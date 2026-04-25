package danmaku

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DandanplayProvider 通过 dandanplay 兼容 API 获取弹幕
type DandanplayProvider struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewDandanplayProvider 创建 dandanplay 弹幕 provider
// baseURL 支持官方 API (https://api.dandanplay.net) 或自部署服务地址
func NewDandanplayProvider(baseURL, token string) *DandanplayProvider {
	return &DandanplayProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// --- API 响应结构 ---

type matchRequest struct {
	FileName  string `json:"fileName"`
	MatchMode string `json:"matchMode"`
}

type matchResponse struct {
	IsMatched bool          `json:"isMatched"`
	Matches   []matchResult `json:"matches"`
	Success   bool          `json:"success"`
}

type matchResult struct {
	EpisodeId    int64  `json:"episodeId"`
	AnimeTitle   string `json:"animeTitle"`
	EpisodeTitle string `json:"episodeTitle"`
}

type searchEpisodesResponse struct {
	HasMore      bool                 `json:"hasMore"`
	Animes       []searchEpisodesAnime `json:"animes"`
	Success      bool                 `json:"success"`
	ErrorCode    int                  `json:"errorCode"`
	ErrorMessage string               `json:"errorMessage"`
}

type searchEpisodesAnime struct {
	AnimeID    int64                  `json:"animeId"`
	AnimeTitle string                 `json:"animeTitle"`
	Episodes   []searchEpisodeDetail  `json:"episodes"`
}

type searchEpisodeDetail struct {
	EpisodeID    int64  `json:"episodeId"`
	EpisodeTitle string `json:"episodeTitle"`
}

type commentResponse struct {
	Count    int           `json:"count"`
	Comments []commentData `json:"comments"`
}

type commentData struct {
	CID int64  `json:"cid"`
	P   string `json:"p"` // "出现时间,模式,颜色,用户ID"
	M   string `json:"m"` // 弹幕内容
}

// --- 公共方法 ---

// MatchEpisode 根据文件名进行弹弹play智能匹配
func (p *DandanplayProvider) MatchEpisode(fileName string) (episodeId int64, title string, err error) {
	apiURL := fmt.Sprintf("%s/api/v2/match", p.baseURL)

	reqData := matchRequest{
		FileName:  fileName,
		MatchMode: "fileNameOnly",
	}
	jsonData, _ := json.Marshal(reqData)

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, "", err
	}
	p.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("match request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("match returned status %d", resp.StatusCode)
	}

	var result matchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", fmt.Errorf("decode match response failed: %w", err)
	}

	if len(result.Matches) > 0 {
		m := result.Matches[0]
		return m.EpisodeId, fmt.Sprintf("%s - %s", m.AnimeTitle, m.EpisodeTitle), nil
	}

	return 0, "", fmt.Errorf("no match found for filename %q", fileName)
}

// SearchEpisode 根据作品名和集数搜索匹配的 episodeId
func (p *DandanplayProvider) SearchEpisode(animeName string, episode int) (episodeId int64, title string, err error) {
	apiURL := fmt.Sprintf("%s/api/v2/search/episodes?anime=%s", p.baseURL, url.QueryEscape(animeName))
	if episode > 0 {
		apiURL += fmt.Sprintf("&episode=%d", episode)
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return 0, "", err
	}
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("search episodes request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("search episodes returned status %d", resp.StatusCode)
	}

	var result searchEpisodesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", fmt.Errorf("decode search response failed: %w", err)
	}

	// 从结果中找到第一个有剧集的作品
	for _, anime := range result.Animes {
		if len(anime.Episodes) > 0 {
			ep := anime.Episodes[0]
			return ep.EpisodeID, fmt.Sprintf("%s - %s", anime.AnimeTitle, ep.EpisodeTitle), nil
		}
	}

	return 0, "", fmt.Errorf("no matching episode found for %q ep %d", animeName, episode)
}

// FetchDanmaku 根据 episodeId 获取弹幕列表
func (p *DandanplayProvider) FetchDanmaku(episodeId int64) ([]Comment, error) {
	apiURL := fmt.Sprintf("%s/api/v2/comment/%d?withRelated=true&chConvert=1", p.baseURL, episodeId)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch danmaku request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch danmaku returned status %d", resp.StatusCode)
	}

	var result commentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode comment response failed: %w", err)
	}

	comments := make([]Comment, 0, len(result.Comments))
	for _, c := range result.Comments {
		comment, ok := parseCommentP(c.P, c.M)
		if ok {
			comments = append(comments, comment)
		}
	}

	return comments, nil
}

// --- 内部方法 ---

func (p *DandanplayProvider) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "simple-emby/1.0")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
}

// parseCommentP 解析弹幕 p 字段: "出现时间,模式,颜色,用户ID"
func parseCommentP(p string, content string) (Comment, bool) {
	parts := strings.Split(p, ",")
	if len(parts) < 3 {
		return Comment{}, false
	}

	t, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return Comment{}, false
	}

	m, _ := strconv.Atoi(parts[1])
	c, _ := strconv.Atoi(parts[2])

	return Comment{
		Time:    t,
		Mode:    Mode(m),
		Size:    25, // dandanplay API 不提供字号，使用默认值
		Color:   c,
		Content: content,
	}, true
}
