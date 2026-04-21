package main

import (
	"compress/flate"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// BiliBangumiResponse 结构体
type BiliBangumiResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Result  struct {
		Episodes []struct {
			Id  int    `json:"id"`
			Cid int    `json:"cid"`
			Title string `json:"title"`
			LongTitle string `json:"long_title"`
		} `json:"episodes"`
	} `json:"result"`
}

type BiliDanmakuXML struct {
	XMLName xml.Name `xml:"i"`
	Items   []struct {
		P       string `xml:"p,attr"`
		Content string `xml:",chardata"`
	} `xml:"d"`
}

func main() {
	epId := "733316"
	fmt.Printf("--- 正在解析番剧弹幕: ep%s ---\n", epId)

	// 1. 获取 CID
	cid, title, err := getCidFromEpId(epId)
	if err != nil {
		log.Fatalf("[Error] 获取番剧 CID 失败: %v", err)
	}
	fmt.Printf("[Success] 视频标题: %s, CID: %d\n", title, cid)

	// 2. 拉取弹幕
	danmakus, err := fetchDanmaku(cid)
	if err != nil {
		log.Fatalf("[Error] 拉取弹幕失败: %v", err)
	}

	// 3. 转换为 ASS 并保存
	assPath := "test/danmaku/danmaku.ass"
	if err := saveToASS(danmakus, assPath); err != nil {
		log.Fatalf("[Error] 保存 ASS 失败: %v", err)
	}

	fmt.Printf("[Success] 转换完成！弹幕已保存至: %s\n", assPath)
	fmt.Printf("[Info] 你可以运行 'mpv --sub-file=%s YOUR_VIDEO_FILE' 来查看效果\n", assPath)
}

func getCidFromEpId(epId string) (int, string, error) {
	apiURL := fmt.Sprintf("https://api.bilibili.com/pgc/view/web/season?ep_id=%s", epId)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	var result BiliBangumiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", err
	}

	targetEpId, _ := strconv.Atoi(epId)
	for _, ep := range result.Result.Episodes {
		if ep.Id == targetEpId {
			return ep.Cid, fmt.Sprintf("%s - %s", ep.Title, ep.LongTitle), nil
		}
	}
	return 0, "", fmt.Errorf("ep_id not found")
}

func fetchDanmaku(cid int) (*BiliDanmakuXML, error) {
	apiURL := fmt.Sprintf("https://comment.bilibili.com/%d.xml", cid)
	fmt.Printf("[Debug] 原始弹幕 XML 链接: %s\n", apiURL)
	
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	zr := flate.NewReader(resp.Body)
	defer zr.Close()

	body, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}

	var danmaku BiliDanmakuXML
	if err := xml.Unmarshal(body, &danmaku); err != nil {
		return nil, err
	}
	return &danmaku, nil
}

// saveToASS 将弹幕结构体转换为 ASS 文件
func saveToASS(dm *BiliDanmakuXML, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// 1. 写入 ASS 头部配置
	header := `[Script Info]
Title: Bilibili Danmaku
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080
Timer: 100.0000

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,45,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`
	f.WriteString(header)

	// 2. 遍历弹幕并转换
	for _, item := range dm.Items {
		// p 格式: time,mode,size,color,timestamp,pool,user_hash,db_id
		parts := strings.Split(item.P, ",")
		if len(parts) < 4 {
			continue
		}

		startTimeSec, _ := strconv.ParseFloat(parts[0], 64)
		mode, _ := strconv.Atoi(parts[1])
		// colorDec, _ := strconv.Atoi(parts[3])

		// 格式化时间为 0:00:00.00
		startStr := formatASSTime(startTimeSec)
		endStr := formatASSTime(startTimeSec + 8.0) // 弹幕在屏幕上停留 8 秒

		// 处理弹幕位置 (简单实现：随机高度，避免重叠需要更复杂的算法)
		yPos := 50 + (int(startTimeSec*100) % 800) // 简易分布在 50-850 像素高度
		
		content := escapeASS(item.Content)
		
		var line string
		if mode <= 3 { // 滚动弹幕
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\move(1920,%d,-500,%d)}%s\n", 
				startStr, endStr, yPos, yPos, content)
		} else if mode == 5 { // 顶部弹幕
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\pos(960,%d)}%s\n", 
				startStr, endStr, yPos, content)
		} else if mode == 4 { // 底部弹幕
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\pos(960,%d)}%s\n", 
				startStr, endStr, 1000-yPos, content)
		}

		f.WriteString(line)
	}

	return nil
}

func formatASSTime(sec float64) string {
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	s := sec - float64(h*3600+m*60)
	return fmt.Sprintf("%d:%02d:%05.2f", h, m, s)
}

func escapeASS(text string) string {
	text = strings.ReplaceAll(text, "{", "｛")
	text = strings.ReplaceAll(text, "}", "｝")
	text = strings.ReplaceAll(text, "\n", "")
	text = strings.ReplaceAll(text, "\r", "")
	return text
}
