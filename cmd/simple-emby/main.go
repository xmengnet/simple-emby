package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"fyne.io/systray"
	"github.com/xmengnet/simple-emby/asset"
	"github.com/xmengnet/simple-emby/internal/config"
	"github.com/xmengnet/simple-emby/internal/emby"
	"github.com/xmengnet/simple-emby/internal/mpv"
	"github.com/xmengnet/simple-emby/internal/server"
)

var (
	appConfig  *config.Config
	sessionMgr *emby.SessionManager
	mpvManager *mpv.Manager
	httpServer *server.Server
	mStatus    *systray.MenuItem
)

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	initLogging()
	systray.SetIcon(asset.Icon)
	systray.SetTitle("Emby mpv")
	systray.SetTooltip("Emby mpv Middleware - Idle")

	// Load Config
	var err error
	appConfig, err = config.LoadConfig()
	if err != nil {
		log.Printf("Failed to load config: %v", err)
		appConfig = &config.DefaultConfig
	}

	// Initialize components
	sessionMgr = emby.NewSessionManager(nil)
	mpvManager = mpv.NewManager(appConfig.MpvPath, appConfig.MpvArgs)
	httpServer = server.NewServer(appConfig, sessionMgr, mpvManager)

	// Register status change callback for tray
	httpServer.SetStatusChangeCallback(func(playing bool, title string) {
		if playing {
			systray.SetTooltip(fmt.Sprintf("Playing: %s", title))
			if mStatus != nil {
				mStatus.SetTitle(fmt.Sprintf("▶ Playing: %s", title))
			}
		} else {
			systray.SetTooltip("Emby mpv Middleware - Idle")
			if mStatus != nil {
				mStatus.SetTitle("⏹ Idle - Waiting for playback")
			}
		}
	})

	// Start HTTP Server
	go func() {
		if err := httpServer.Start(); err != nil {
			log.Printf("HTTP server stopped: %v", err)
		}
	}()

	// Setup Tray Menu
	mStatus = systray.AddMenuItem("⏹ Idle - Waiting for playback", "Current playback status")
	mStatus.Disable()

	systray.AddSeparator()

	mOpenConfig := systray.AddMenuItem("Open Config Dir", "Open configuration directory")

	mEnableDanmaku := systray.AddMenuItemCheckbox("Enable Danmaku", "Enable or disable danmaku", appConfig.EnableDanmaku)

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit the whole app")

	go func() {
		for {
			select {
			case <-mOpenConfig.ClickedCh:
				configPath, _ := config.GetConfigPath()
				go func() {
					cmd := exec.Command("xdg-open", filepath.Dir(configPath))
					_ = cmd.Run()
				}()
			case <-mEnableDanmaku.ClickedCh:
				if mEnableDanmaku.Checked() {
					mEnableDanmaku.Uncheck()
					appConfig.EnableDanmaku = false
				} else {
					mEnableDanmaku.Check()
					appConfig.EnableDanmaku = true
				}
				_ = config.SaveConfig(appConfig)
				log.Printf("Danmaku state changed: %v", appConfig.EnableDanmaku)
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {
	log.Println("Shutting down...")
	if httpServer != nil {
		httpServer.Stop()
	}
	if mpvManager != nil {
		mpvManager.Stop()
	}
	if sessionMgr != nil {
		sessionMgr.StopSession()
	}
}

func initLogging() {
	logPath, err := config.GetLogPath()
	if err != nil {
		log.Printf("Failed to get log path: %v", err)
		return
	}

	// Create config dir if not exists
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file: %v", err)
		return
	}

	// MultiWriter outputs to both stdout and the file
	mw := io.MultiWriter(os.Stdout, f)
	log.SetOutput(mw)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println("--- Simple Emby Log Started ---")
}
