package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/xmengnet/simple-emby/internal/config"
	"github.com/xmengnet/simple-emby/internal/danmaku"
	"github.com/xmengnet/simple-emby/internal/emby"
	"github.com/xmengnet/simple-emby/internal/mpv"
)

// StatusChangeFunc is called when playback status changes (playing bool, title string)
type StatusChangeFunc func(playing bool, title string)

type Server struct {
	cfg            *config.Config
	sessionMgr     *emby.SessionManager
	mpvManager     *mpv.Manager
	httpServer     *http.Server
	onStatusChange StatusChangeFunc

	// Current playback context for auto next-episode
	mu            sync.Mutex
	currentClient *emby.Client
	currentItem   *emby.ItemInfo
}

func (s *Server) SetStatusChangeCallback(cb StatusChangeFunc) {
	s.onStatusChange = cb
}

func NewServer(cfg *config.Config, sessionMgr *emby.SessionManager, mpvManager *mpv.Manager) *Server {
	return &Server{
		cfg:        cfg,
		sessionMgr: sessionMgr,
		mpvManager: mpvManager,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/play", s.handlePlay)

	s.httpServer = &http.Server{
		Addr:    s.cfg.BindAddr,
		Handler: mux,
	}

	log.Printf("Starting local HTTP server on %s", s.cfg.BindAddr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Stop() {
	if s.httpServer != nil {
		s.httpServer.Close()
	}
}

// PlayRequest represents the JSON payload from the frontend
type PlayRequest struct {
	ServerURL  string `json:"server_url"`
	APIKey     string `json:"api_key"`
	UserId     string `json:"user_id"`
	ItemId     string `json:"item_id"`
	MediaTitle string `json:"media_title"`
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	// Enable CORS for frontend script
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PlayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.ItemId == "" || req.ServerURL == "" || req.APIKey == "" || req.UserId == "" {
		http.Error(w, "Missing required parameters in payload", http.StatusBadRequest)
		return
	}

	log.Printf("Received play request for Item ID: %s, Title: %s", req.ItemId, req.MediaTitle)

	client := emby.NewClient(req.ServerURL, req.APIKey, req.UserId)
	if err := s.startPlayback(client, req.ItemId, req.MediaTitle); err != nil {
		log.Printf("Error starting playback: %v", err)
		http.Error(w, fmt.Sprintf("Failed to start playback: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Playing..."))
}

// startPlayback is the core method that handles the full play flow including next-episode.
func (s *Server) startPlayback(client *emby.Client, itemId string, mediaTitle string) error {
	// 1. Get Playback Info
	pbInfo, err := client.GetPlaybackInfo(itemId)
	if err != nil {
		return fmt.Errorf("failed to get playback info: %w", err)
	}

	// 2. Get item info (resume position + episode metadata for auto-next)
	itemInfo, err := client.GetItemInfo(itemId)
	if err != nil {
		log.Printf("Warning: could not get item info: %v", err)
		itemInfo = &emby.ItemInfo{Id: itemId, Name: mediaTitle}
	}

	// Build title from item info if not provided
	if mediaTitle == "" {
		mediaTitle = itemInfo.Name
	}

	// 3. Calculate resume start position
	var startPositionSec float64
	if itemInfo.UserData.PlaybackPositionTicks > 0 {
		startPositionSec = float64(itemInfo.UserData.PlaybackPositionTicks) / 10000000.0
		log.Printf("Resuming from %.1fs", startPositionSec)
	}

	// 4. Store current context for auto-next
	mediaSourceId := pbInfo.MediaSources[0].Id
	streamURL := client.ConstructStreamURL(itemId, mediaSourceId)

	s.mu.Lock()
	s.currentClient = client
	s.currentItem = itemInfo
	s.mu.Unlock()

	// 5. Update session manager client and notify tray
	s.sessionMgr.SetClient(client)
	if s.onStatusChange != nil {
		s.onStatusChange(true, mediaTitle)
	}

	// 6. Try to match and download danmaku
	var subFile string
	if s.cfg.EnableDanmaku {
		danmakuDir, _ := config.GetDanmakuPath()
		provider := danmaku.NewBiliProvider()

		// Create search key: "SeriesName 第IndexNumber集"
		searchKey := fmt.Sprintf("%s 第%d集", itemInfo.SeriesName, itemInfo.IndexNumber)
		if itemInfo.IndexNumber == 0 {
			searchKey = itemInfo.Name
		}

		log.Printf("Searching danmaku for: %s", searchKey)
		bvid, title, err := provider.Search(searchKey)
		if err == nil {
			if cid, err := provider.GetCid(bvid); err == nil {
				if comments, err := provider.FetchDanmaku(cid); err == nil {
					subPath := filepath.Join(danmakuDir, fmt.Sprintf("%s.ass", itemId))
					dm := &danmaku.Danmaku{Title: title, Comments: comments}
					if err := danmaku.RenderToASS(dm, subPath); err == nil {
						subFile = subPath
						log.Printf("Successfully matched danmaku: %s -> %s", title, subPath)
					}
				}
			}
		} else {
			log.Printf("No danmaku matched for %s: %v", searchKey, err)
		}
	} else {
		log.Println("Danmaku is disabled in config.")
	}

	// 7. Launch mpv with event callbacks
	err = s.mpvManager.Play(streamURL, mediaTitle, startPositionSec, subFile, func(event string, data interface{}) {
		switch event {
		case "time-pos":
			if pos, ok := data.(float64); ok {
				s.sessionMgr.UpdatePosition(pos)
			}
		case "pause":
			if paused, ok := data.(bool); ok {
				s.sessionMgr.UpdatePauseState(paused)
			}
		case "eof":
			// EOF = natural end of file, try auto-next episode
			s.sessionMgr.StopSession()
			go s.tryPlayNextEpisode()
		case "process-exit":
			// mpv was quit manually or crashed
			s.sessionMgr.StopSession()
			if s.onStatusChange != nil {
				s.onStatusChange(false, "")
			}
		}
	})

	if err != nil {
		if s.onStatusChange != nil {
			s.onStatusChange(false, "")
		}
		return err
	}

	// 7. Notify Emby that playback has started (after mpv is up)
	s.sessionMgr.StartSession(itemId, mediaSourceId)
	return nil
}

// tryPlayNextEpisode is called after EOF. Looks up and plays the next episode.
func (s *Server) tryPlayNextEpisode() {
	s.mu.Lock()
	client := s.currentClient
	item := s.currentItem
	s.mu.Unlock()

	if client == nil || item == nil || item.SeasonId == "" {
		log.Println("No episode context available for auto-next, stopping.")
		if s.onStatusChange != nil {
			s.onStatusChange(false, "")
		}
		return
	}

	log.Printf("Looking for next episode after S%02dE%02d...", item.ParentIndexNumber, item.IndexNumber)

	nextEp, err := client.GetNextEpisode(item.SeasonId, item.IndexNumber)
	if err != nil {
		log.Printf("Error fetching next episode: %v", err)
		if s.onStatusChange != nil {
			s.onStatusChange(false, "")
		}
		return
	}

	if nextEp == nil {
		log.Println("No next episode found (end of season).")
		if s.onStatusChange != nil {
			s.onStatusChange(false, "")
		}
		return
	}

	title := fmt.Sprintf("%s - S%02dE%02d - %s",
		nextEp.SeriesName, nextEp.ParentIndexNumber, nextEp.IndexNumber, nextEp.Name)
	log.Printf("Auto-playing next episode: %s", title)

	if err := s.startPlayback(client, nextEp.Id, title); err != nil {
		log.Printf("Failed to auto-play next episode: %v", err)
		if s.onStatusChange != nil {
			s.onStatusChange(false, "")
		}
	}
}
