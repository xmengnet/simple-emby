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

	// 6. Launch mpv with event callbacks FIRST
	err = s.mpvManager.Play(streamURL, mediaTitle, startPositionSec, "", func(event string, data interface{}) {
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

	// 8. Try to match and download danmaku asynchronously
	if s.cfg.EnableDanmaku {
		go func(targetItemId string, item *emby.ItemInfo) {
			danmakuDir, _ := config.GetDanmakuPath()
			provider := danmaku.NewDandanplayProvider(s.cfg.DandanplayAPI, s.cfg.DandanplayToken)

			var episodeId int64
			var title string
			var matchErr error

			// Strategy 1: Real filename
			if item.Path != "" {
				fileName := filepath.Base(item.Path)
				log.Printf("Async Danmaku Strategy 1: Matching using real filename: %s", fileName)
				episodeId, title, matchErr = provider.MatchEpisode(fileName)
			} else {
				matchErr = fmt.Errorf("no path available")
			}

			// Strategy 2: Fake standard filename
			if matchErr != nil {
				animeName := item.SeriesName
				if animeName == "" {
					animeName = item.Name
				}
				fakeName := fmt.Sprintf("%s S%02dE%02d.mp4", animeName, item.ParentIndexNumber, item.IndexNumber)
				log.Printf("Async Danmaku Strategy 2: Matching using standard filename: %s", fakeName)
				episodeId, title, matchErr = provider.MatchEpisode(fakeName)
			}

			// Strategy 3: Fallback to Search
			if matchErr != nil {
				animeName := item.SeriesName
				if animeName == "" {
					animeName = item.Name
				}
				log.Printf("Async Danmaku Strategy 3: Searching for: %s ep %d", animeName, item.IndexNumber)
				episodeId, title, matchErr = provider.SearchEpisode(animeName, item.IndexNumber)
			}

			if matchErr == nil {
				if comments, fetchErr := provider.FetchDanmaku(episodeId); fetchErr == nil {
					subPath := filepath.Join(danmakuDir, fmt.Sprintf("%s.ass", targetItemId))
					dm := &danmaku.Danmaku{Title: title, Comments: comments}
					if err := danmaku.RenderToASS(dm, subPath); err == nil {
						log.Printf("Successfully matched async danmaku: %s (%d comments) -> %s", title, len(comments), subPath)
						
						// Verify that the user hasn't switched to another episode while downloading
						s.mu.Lock()
						currentId := ""
						if s.currentItem != nil {
							currentId = s.currentItem.Id
						}
						s.mu.Unlock()
						
						if currentId == targetItemId {
							log.Println("Applying async danmaku to running mpv instance.")
							s.mpvManager.AddSubtitle(subPath)
						} else {
							log.Println("Playback context changed during danmaku fetch, discarding.")
						}
					}
				} else {
					log.Printf("Failed to fetch async danmaku for episodeId %d: %v", episodeId, fetchErr)
				}
			} else {
				animeName := item.SeriesName
				if animeName == "" {
					animeName = item.Name
				}
				log.Printf("All async danmaku match strategies failed for %s ep %d: %v", animeName, item.IndexNumber, matchErr)
			}
		}(itemId, itemInfo)
	} else {
		log.Println("Danmaku is disabled in config.")
	}

	return nil
}

// tryPlayNextEpisode is called after EOF. Looks up and plays the next episode.
func (s *Server) tryPlayNextEpisode() {
	s.mu.Lock()
	client := s.currentClient
	item := s.currentItem
	s.mu.Unlock()

	if client == nil || item == nil || item.SeriesId == "" {
		log.Println("No episode context available for auto-next, stopping.")
		if s.onStatusChange != nil {
			s.onStatusChange(false, "")
		}
		return
	}

	log.Printf("Looking for next episode after S%02dE%02d...", item.ParentIndexNumber, item.IndexNumber)

	nextEp, err := client.GetNextEpisode(item.SeriesId, item.SeasonId, item.IndexNumber)
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
