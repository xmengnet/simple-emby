package danmaku

import (
	"fmt"
	"os"
)

const (
	ASSHeader = `[Script Info]
Title: %s
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
)

// RenderToASS 将弹幕渲染并保存为 ASS 文件
func RenderToASS(dm *Danmaku, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// 1. 写入头部
	f.WriteString(fmt.Sprintf(ASSHeader, dm.Title))

	// 2. 写入弹幕事件
	for _, c := range dm.Comments {
		startStr := FormatASSTime(c.Time)
		endStr := FormatASSTime(c.Time + 8.0) // 默认停留 8 秒

		// 简单的轨道分配逻辑（基于时间取模，防止重叠）
		yPos := 50 + (int(c.Time*100) % 800)
		
		content := EscapeASS(c.Content)
		
		var line string
		switch c.Mode {
		case ModeRolling:
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\move(1920,%d,-500,%d)}%s\n", 
				startStr, endStr, yPos, yPos, content)
		case ModeTop:
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\pos(960,%d)}%s\n", 
				startStr, endStr, yPos, content)
		case ModeBottom:
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\pos(960,%d)}%s\n", 
				startStr, endStr, 1000-yPos, content)
		default:
			// 默认按滚动处理
			line = fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,{\\move(1920,%d,-500,%d)}%s\n", 
				startStr, endStr, yPos, yPos, content)
		}

		f.WriteString(line)
	}

	return nil
}
