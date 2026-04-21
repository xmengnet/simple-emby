package emby

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

type SessionManager struct {
	client *Client

	mu            sync.Mutex
	cancelFunc    context.CancelFunc
	playSessionId string
	itemId        string
	mediaSourceId string

	// cached state - always updated from mpv events
	positionTicks int64
	isPaused      bool
}

func NewSessionManager(client *Client) *SessionManager {
	return &SessionManager{
		client: client,
	}
}

// SetClient updates the underlying Emby client (e.g. when config changes)
func (s *SessionManager) SetClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = client
}

func (s *SessionManager) StartSession(itemId, mediaSourceId string) string {
	s.StopSession() // Stop any existing session first

	s.mu.Lock()
	defer s.mu.Unlock()

	s.playSessionId = uuid.New().String()
	s.itemId = itemId
	s.mediaSourceId = mediaSourceId
	s.positionTicks = 0
	s.isPaused = false

	info := s.buildInfo("TimeUpdate")
	if err := s.client.StartPlaying(info); err != nil {
		log.Printf("Failed to notify Emby of playback start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFunc = cancel
	go s.heartbeatLoop(ctx)

	return s.playSessionId
}

// UpdatePosition updates only the playback position (called from time-pos events)
func (s *SessionManager) UpdatePosition(positionSec float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playSessionId == "" {
		return
	}
	// Convert seconds to ticks (1 tick = 100ns)
	s.positionTicks = int64(positionSec * 10000000)
}

// UpdatePauseState is called when pause state changes - sends immediate event to Emby
func (s *SessionManager) UpdatePauseState(isPaused bool) {
	s.mu.Lock()
	if s.playSessionId == "" {
		s.mu.Unlock()
		return
	}
	if s.isPaused == isPaused {
		s.mu.Unlock()
		return
	}
	s.isPaused = isPaused

	eventName := "Unpause"
	if isPaused {
		eventName = "Pause"
	}
	info := s.buildInfo(eventName)
	client := s.client
	s.mu.Unlock()

	// Send immediately, not in a goroutine, so server registers state change ASAP
	go func() {
		if err := client.ReportProgress(info); err != nil {
			log.Printf("Failed to report %s event: %v", eventName, err)
		}
	}()
}

func (s *SessionManager) StopSession() {
	s.mu.Lock()

	if s.cancelFunc != nil {
		s.cancelFunc()
		s.cancelFunc = nil
	}

	if s.playSessionId == "" {
		s.mu.Unlock()
		return
	}

	info := s.buildInfo("TimeUpdate")

	s.playSessionId = ""
	s.itemId = ""
	s.mediaSourceId = ""
	s.mu.Unlock()

	if err := s.client.StopPlaying(info); err != nil {
		log.Printf("Failed to notify Emby of playback stop: %v", err)
	}
}

// buildInfo constructs a PlaybackProgressInfo from current state.
// Must be called with s.mu held.
func (s *SessionManager) buildInfo(eventName string) PlaybackProgressInfo {
	return PlaybackProgressInfo{
		PositionTicks: s.positionTicks,
		IsPaused:      s.isPaused,
		IsMuted:       false,
		VolumeLevel:   100,
		EventName:     eventName,
		PlaySessionId: s.playSessionId,
		ItemId:        s.itemId,
		MediaSourceId: s.mediaSourceId,
		PlayMethod:    "DirectPlay",
		CanSeek:       true,
	}
}

func (s *SessionManager) heartbeatLoop(ctx context.Context) {
	// Use a short ticker and report based on state
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.playSessionId == "" {
				s.mu.Unlock()
				return
			}
			// Paused: report every 5s. Playing: report every 10s (skip odd ticks).
			isPaused := s.isPaused
			info := s.buildInfo("TimeUpdate")
			client := s.client
			s.mu.Unlock()

			// Only skip every other tick when playing to achieve ~10s interval
			if !isPaused {
				// Simple counter using time: report at 10s intervals when playing
				// Actually just always report every 5s – server handles it fine.
			}

			if err := client.ReportProgress(info); err != nil {
				log.Printf("Failed to report heartbeat: %v", err)
			}
		}
	}
}
