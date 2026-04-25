package emby

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	BaseURL    string
	APIKey     string
	UserId     string
	HTTPClient *http.Client
}

func NewClient(baseURL, apiKey, userId string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		UserId:  userId,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) setAuthHeaders(req *http.Request) {
	req.Header.Set("X-Emby-Authorization", fmt.Sprintf(
		`MediaBrowser Client="simple-emby", Device="Linux", DeviceId="simple-emby-linux", Version="1.0", Token="%s"`,
		c.APIKey,
	))
	req.Header.Set("X-Emby-Token", c.APIKey)
	// 伪装浏览器 UA 避免被防火墙或 WAF 直接 403 拦截
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
}

// MediaSourceInfo represents a media source from Emby
type MediaSourceInfo struct {
	Id                   string `json:"Id"`
	Path                 string `json:"Path"`
	SupportsDirectStream bool   `json:"SupportsDirectStream"`
	SupportsDirectPlay   bool   `json:"SupportsDirectPlay"`
}

// PlaybackInfoResponse represents the response from /Items/{Id}/PlaybackInfo
type PlaybackInfoResponse struct {
	MediaSources []MediaSourceInfo `json:"MediaSources"`
}

func (c *Client) GetPlaybackInfo(itemId string) (*PlaybackInfoResponse, error) {
	apiURL := fmt.Sprintf("%s/emby/Items/%s/PlaybackInfo?UserId=%s&api_key=%s", c.BaseURL, itemId, c.UserId, c.APIKey)

	// Emby 4.9+ 和一些反代要求该接口必须是 POST，并且有些需要 JSON body
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get playback info, status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var pbInfo PlaybackInfoResponse
	if err := json.Unmarshal(body, &pbInfo); err != nil {
		return nil, err
	}

	if len(pbInfo.MediaSources) == 0 {
		return nil, fmt.Errorf("no media sources found for item %s", itemId)
	}

	return &pbInfo, nil
}

func (c *Client) ConstructStreamURL(itemId, mediaSourceId string) string {
	query := url.Values{}
	query.Add("api_key", c.APIKey)
	query.Add("MediaSourceId", mediaSourceId)
	query.Add("static", "true")

	return fmt.Sprintf("%s/emby/Videos/%s/stream?%s", c.BaseURL, itemId, query.Encode())
}

// ItemUserData contains the user's playback state for an item
type ItemUserData struct {
	PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
	Played                bool  `json:"Played"`
}

// ItemInfo contains basic item metadata
type ItemInfo struct {
	Id                string       `json:"Id"`
	Name              string       `json:"Name"`
	Type              string       `json:"Type"`
	SeriesId          string       `json:"SeriesId"`
	SeriesName        string       `json:"SeriesName"`
	SeasonId          string       `json:"SeasonId"`
	IndexNumber       int          `json:"IndexNumber"`       // episode number
	ParentIndexNumber int          `json:"ParentIndexNumber"` // season number
	RunTimeTicks      int64        `json:"RunTimeTicks"`
	UserData          ItemUserData `json:"UserData"`
	Path              string       `json:"Path"`
}

// GetItemInfo fetches item metadata including resume position and file path
func (c *Client) GetItemInfo(itemId string) (*ItemInfo, error) {
	apiURL := fmt.Sprintf("%s/emby/Users/%s/Items/%s?api_key=%s&Fields=Path", c.BaseURL, c.UserId, itemId, c.APIKey)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	c.setAuthHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get item info, status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var info ItemInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

// PlaybackProgressInfo represents the payload for progress heartbeat
type PlaybackProgressInfo struct {
	PositionTicks       int64  `json:"PositionTicks"`
	IsPaused            bool   `json:"IsPaused"`
	IsMuted             bool   `json:"IsMuted"`
	VolumeLevel         int    `json:"VolumeLevel"`
	EventName           string `json:"EventName"`
	PlaySessionId       string `json:"PlaySessionId"`
	ItemId              string `json:"ItemId"`
	MediaSourceId       string `json:"MediaSourceId"`
	PlayMethod          string `json:"PlayMethod"`
	CanSeek             bool   `json:"CanSeek"`
	AudioStreamIndex    int    `json:"AudioStreamIndex"`
	SubtitleStreamIndex int    `json:"SubtitleStreamIndex"`
}

func (c *Client) StartPlaying(info PlaybackProgressInfo) error {
	return c.sendProgressEvent("Playing", info)
}

func (c *Client) ReportProgress(info PlaybackProgressInfo) error {
	return c.sendProgressEvent("Playing/Progress", info)
}

func (c *Client) StopPlaying(info PlaybackProgressInfo) error {
	return c.sendProgressEvent("Playing/Stopped", info)
}

func (c *Client) sendProgressEvent(endpoint string, info PlaybackProgressInfo) error {
	apiURL := fmt.Sprintf("%s/emby/Sessions/%s", c.BaseURL, endpoint)

	jsonData, err := json.Marshal(info)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to send progress to %s: status %d, response: %s", endpoint, resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// EpisodeItem is a compact episode representation from the episodes list
type EpisodeItem struct {
	Id                string `json:"Id"`
	Name              string `json:"Name"`
	IndexNumber       int    `json:"IndexNumber"`
	SeriesName        string `json:"SeriesName"`
	ParentIndexNumber int    `json:"ParentIndexNumber"`
}

type EpisodesResponse struct {
	Items []EpisodeItem `json:"Items"`
}

// GetNextEpisode finds the next episode in the same series/season after currentIndex.
// Returns nil, nil if there is no next episode.
func (c *Client) GetNextEpisode(seriesId, seasonId string, currentIndex int) (*EpisodeItem, error) {
	apiURL := fmt.Sprintf("%s/emby/Shows/%s/Episodes?SeasonId=%s&UserId=%s&api_key=%s&Fields=MediaSources",
		c.BaseURL, seriesId, seasonId, c.UserId, c.APIKey)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	c.setAuthHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get episodes, status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var epResp EpisodesResponse
	if err := json.Unmarshal(body, &epResp); err != nil {
		return nil, err
	}

	for _, ep := range epResp.Items {
		if ep.IndexNumber == currentIndex+1 {
			return &ep, nil
		}
	}
	return nil, nil
}
