package mpv

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dexterlb/mpvipc"
)

type EventCallback func(event string, data interface{})

type Manager struct {
	mpvPath  string
	cmd      *exec.Cmd
	ipcConn  *mpvipc.Connection
	socket   string
	mu       sync.Mutex
	callback EventCallback
	mpvArgs  []string
}

func NewManager(mpvPath string, mpvArgs string) *Manager {
	var args []string
	if mpvArgs != "" {
		args = strings.Fields(mpvArgs)
	}
	return &Manager{
		mpvPath: mpvPath,
		socket:  "/tmp/mpv-emby.sock",
		mpvArgs: args,
	}
}

// SetMpvPath updates the path to the mpv executable
func (m *Manager) SetMpvPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mpvPath = path
}

func (m *Manager) Play(streamURL string, title string, startPositionSec float64, cb EventCallback) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.callback = cb

	// If we already have a process and IPC, try loading the new file
	if m.cmd != nil && m.ipcConn != nil {
		_, err := m.ipcConn.Call("loadfile", streamURL)
		if err == nil {
			log.Println("Successfully loaded new stream into existing mpv instance.")
			if title != "" {
				_, _ = m.ipcConn.Call("set_property", "force-media-title", title)
			}
			if startPositionSec > 0 {
				// Wait briefly for file to load, then seek
				go func() {
					time.Sleep(1 * time.Second)
					m.mu.Lock()
					defer m.mu.Unlock()
					if m.ipcConn != nil {
						_, _ = m.ipcConn.Call("set_property", "time-pos", startPositionSec)
					}
				}()
			}
			return nil
		}
		log.Printf("Failed to load file into existing mpv (might be dead): %v. Restarting mpv...", err)
		m.stopInternal()
	}

	// Start new process
	// Base required arguments
	args := []string{
		streamURL,
		"--input-ipc-server=" + m.socket,
		"--idle=yes",
		"--cache-secs=120",
		"--cache-on-disk=no",
		"--icc-cache=no",
	}
	
	// Add user custom args from config
	args = append(args, m.mpvArgs...)

	if title != "" {
		args = append(args, "--force-media-title="+title)
	}
	if startPositionSec > 0 {
		args = append(args, fmt.Sprintf("--start=%f", startPositionSec))
	}
	
	log.Printf("Starting mpv with args: %v", args)
	m.cmd = exec.Command(m.mpvPath, args...)
	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mpv: %w", err)
	}

	// Wait a bit for socket to be created
	time.Sleep(500 * time.Millisecond)

	m.ipcConn = mpvipc.NewConnection(m.socket)
	if err := m.ipcConn.Open(); err != nil {
		m.stopInternal()
		return fmt.Errorf("failed to connect to mpv IPC: %w", err)
	}

	log.Println("Successfully started mpv and connected to IPC.")

	// Setup event listening
	go m.listenEvents()

	// Wait for process to exit
	go func() {
		err := m.cmd.Wait()
		log.Printf("mpv process exited. Error: %v", err)
		m.mu.Lock()
		if m.callback != nil {
			m.callback("process-exit", nil)
		}
		m.stopInternal()
		m.mu.Unlock()
	}()

	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopInternal()
}

func (m *Manager) stopInternal() {
	if m.ipcConn != nil {
		_, _ = m.ipcConn.Call("quit")
		m.ipcConn.Close()
		m.ipcConn = nil
	}
	if m.cmd != nil {
		if m.cmd.Process != nil {
			_ = m.cmd.Process.Kill()
		}
		m.cmd = nil
	}
}

func (m *Manager) listenEvents() {
	m.mu.Lock()
	conn := m.ipcConn
	m.mu.Unlock()

	if conn == nil {
		return
	}

	events, stop := conn.NewEventListener()
	defer func() {
		if stop != nil {
			close(stop)
		}
	}()

	// Observe properties we care about
	_, _ = conn.Call("observe_property", 1, "time-pos")
	_, _ = conn.Call("observe_property", 2, "pause")

	for event := range events {
		m.mu.Lock()
		cb := m.callback
		m.mu.Unlock()

		if cb == nil {
			continue
		}

		switch event.Name {
		case "property-change":
			if event.ID == 1 {
				if pos, ok := event.Data.(float64); ok {
					cb("time-pos", pos)
				}
			} else if event.ID == 2 {
				if paused, ok := event.Data.(bool); ok {
					cb("pause", paused)
				}
			}
		case "end-file":
			if event.Reason == "eof" {
				cb("eof", nil)
			}
		}
	}
}
