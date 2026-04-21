package danmaku

import (
	"compress/flate"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type BiliProvider struct {
	client *http.Client
}

func NewBiliProvider() *BiliProvider {
	return &BiliProvider{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// BiliSearchResponse 结构体
type BiliSearchResponse struct {
	Code int `json:"code"`
	Data struct {
		Result []struct {
			Title    string `json:"title"`
			Bvid     string `json:"bvid"`
			Duration string `json:"duration"`
		} `json:"result"`
	} `json:"data"`
}

// Search 根据关键字搜索最匹配的正片 BVID
func (p *BiliProvider) Search(keyword string) (string, string, error) {
	apiURL := fmt.Sprintf("https://api.bilibili.com/x/web-interface/search/type?search_type=video&keyword=%s", url.QueryEscape(keyword))
	
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.bilibili.com/")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var result BiliSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}

	for _, res := range result.Data.Result {
		title := strings.ToLower(res.Title)
		// 简单的过滤逻辑
		if strings.Contains(title, "reaction") || strings.Contains(title, "解说") {
			continue
		}
		// 时长过滤 (MM:SS)
		if !strings.Contains(res.Duration, ":") {
			continue
		}
		
		cleanTitle := strings.ReplaceAll(res.Title, "<em class=\"keyword\">", "")
		cleanTitle = strings.ReplaceAll(cleanTitle, "</em>", "")
		return res.Bvid, cleanTitle, nil
	}

	return "", "", fmt.Errorf("no matching video found")
}

// GetCid 获取普通视频的 CID
func (p *BiliProvider) GetCid(bvid string) (int, error) {
	apiURL := fmt.Sprintf("https://api.bilibili.com/x/player/pagelist?bvid=%s", bvid)
	resp, err := p.client.Get(apiURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result struct {
		Code int `json:"code"`
		Data []struct {
			Cid int `json:"cid"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if len(result.Data) == 0 {
		return 0, fmt.Errorf("no cid found")
	}
	return result.Data[0].Cid, nil
}

// GetCidFromEpId 获取番剧视频的 CID
func (p *BiliProvider) GetCidFromEpId(epId string) (int, string, error) {
	apiURL := fmt.Sprintf("https://api.bilibili.com/pgc/view/web/season?ep_id=%s", epId)
	resp, err := p.client.Get(apiURL)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code   int    `json:"code"`
		Message string `json:"message"`
		Result struct {
			Episodes []struct {
				Id    int    `json:"id"`
				Cid   int    `json:"cid"`
				Title string `json:"title"`
				Long  string `json:"long_title"`
			} `json:"episodes"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", err
	}

	targetEp, _ := strconv.Atoi(epId)
	for _, ep := range result.Result.Episodes {
		if ep.Id == targetEp {
			return ep.Cid, fmt.Sprintf("%s - %s", ep.Title, ep.Long), nil
		}
	}
	return 0, "", fmt.Errorf("ep_id %s not found", epId)
}

// FetchDanmaku 拉取并解析弹幕
func (p *BiliProvider) FetchDanmaku(cid int) ([]Comment, error) {
	apiURL := fmt.Sprintf("https://comment.bilibili.com/%d.xml", cid)
	resp, err := p.client.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	zr := flate.NewReader(resp.Body)
	defer zr.Close()

	var xmlData struct {
		Items []struct {
			P       string `xml:"p,attr"`
			Content string `xml:",chardata"`
		} `xml:"d"`
	}
	if err := xml.NewDecoder(zr).Decode(&xmlData); err != nil {
		return nil, err
	}

	comments := make([]Comment, 0, len(xmlData.Items))
	for _, item := range xmlData.Items {
		parts := strings.Split(item.P, ",")
		if len(parts) < 4 {
			continue
		}
		t, _ := strconv.ParseFloat(parts[0], 64)
		m, _ := strconv.Atoi(parts[1])
		s, _ := strconv.Atoi(parts[2])
		c, _ := strconv.Atoi(parts[3])

		comments = append(comments, Comment{
			Time:    t,
			Mode:    Mode(m),
			Size:    s,
			Color:   c,
			Content: item.Content,
		})
	}
	return comments, nil
}
