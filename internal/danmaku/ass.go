package danmaku

import (
	"fmt"
	"os"
	"sort"
)

const (
	// PlayResX/Y 使用 720p 作为 ASS 逻辑分辨率。
	// libass 会自动将所有坐标和字号等比缩放到实际窗口尺寸，
	// 不论是 1080p 还是 2K 都能正确显示，且 GPU 计算量降低约 50%。
	ASSHeader = `[Script Info]
Title: %s
ScriptType: v4.00+
PlayResX: 1280
PlayResY: 720
Timer: 100.0000

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,36,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`
)

// 基于 720p 逻辑分辨率的布局常量
const (
	resY        = 720  // ASS 逻辑高度
	resX        = 1280 // ASS 逻辑宽度
	trackH      = 40   // 轨道高度 (字号36 + 间距4)
	marginTop   = 33   // 顶部起始 Y
	marginBot   = 33   // 底部边距
)

// allocTrack 分配一个可用的弹幕轨道。如果全满则返回 -1
func allocTrack(tracks []float64, time float64, delay float64) int {
	for i, availableAt := range tracks {
		if time >= availableAt {
			tracks[i] = time + delay
			return i
		}
	}
	return -1 // 轨道已满
}

// RenderToASS 将弹幕渲染并保存为 ASS 文件
func RenderToASS(dm *Danmaku, path string) error {
	// 按照时间排序以保证轨道分配算法正常工作
	sort.Slice(dm.Comments, func(i, j int) bool {
		return dm.Comments[i].Time < dm.Comments[j].Time
	})

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// 1. 写入头部
	f.WriteString(fmt.Sprintf(ASSHeader, dm.Title))

	// 弹幕轨道管理器（坐标均基于 720p 逻辑分辨率）
	rollingTracks := make([]float64, 8) // 8 条滚动轨道，满屏丢弃降低 GPU 负载
	topTracks := make([]float64, 4)     // 4 条顶部轨道
	bottomTracks := make([]float64, 4)  // 4 条底部轨道

	// 2. 写入弹幕事件
	for _, c := range dm.Comments {
		startStr := FormatASSTime(c.Time)
		endStr := FormatASSTime(c.Time + 6.0) // 滚动默认停留 6 秒

		content := EscapeASS(c.Content)
		if content == "" {
			continue
		}

		var line string
		switch c.Mode {
		case ModeTop:
			endStr = FormatASSTime(c.Time + 4.0) // 固定弹幕停留 4 秒
			trackIdx := allocTrack(topTracks, c.Time, 4.0)
			if trackIdx == -1 {
				continue // 轨道满了，丢弃
			}
			yPos := marginTop + trackIdx*trackH
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\pos(%d,%d)}%s\n",
				startStr, endStr, resX/2, yPos, content)

		case ModeBottom:
			endStr = FormatASSTime(c.Time + 4.0)
			trackIdx := allocTrack(bottomTracks, c.Time, 4.0)
			if trackIdx == -1 {
				continue
			}
			yPos := resY - marginBot - trackIdx*trackH
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\pos(%d,%d)}%s\n",
				startStr, endStr, resX/2, yPos, content)

		default: // ModeRolling
			// 等前一条弹幕基本离开右边缘后再放行
			delay := 2.0
			if len([]rune(c.Content)) > 10 {
				delay = 3.0 // 长文本多等一会
			}
			trackIdx := allocTrack(rollingTracks, c.Time, delay)
			if trackIdx == -1 {
				continue // 满屏则丢弃
			}
			yPos := marginTop + trackIdx*trackH
			// 起点 resX，终点 -200（比文字宽度略左，更快离屏）
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\move(%d,%d,-200,%d)}%s\n",
				startStr, endStr, resX, yPos, yPos, content)
		}

		f.WriteString(line)
	}

	return nil
}
