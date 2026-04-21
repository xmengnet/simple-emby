package danmaku

import (
	"fmt"
	"strings"
)

// Mode 弹幕模式
type Mode int

const (
	ModeRolling Mode = 1 // 滚动
	ModeBottom  Mode = 4 // 底部
	ModeTop     Mode = 5 // 顶部
)

// Comment 统一的弹幕单条信息
type Comment struct {
	Time    float64 // 出现时间 (秒)
	Mode    Mode    // 模式
	Size    int     // 字号
	Color   int     // 颜色 (十进制)
	Content string  // 内容
}

// Danmaku 弹幕集合
type Danmaku struct {
	Title    string
	Comments []Comment
}

// FormatASSTime 将秒数转换为 ASS 格式: H:MM:SS.CC
func FormatASSTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	s := sec - float64(h*3600+m*60)
	return fmt.Sprintf("%d:%02d:%05.2f", h, m, s)
}

// EscapeASS 转义 ASS 特殊字符
func EscapeASS(text string) string {
	text = strings.ReplaceAll(text, "{", "｛")
	text = strings.ReplaceAll(text, "}", "｝")
	text = strings.ReplaceAll(text, "\n", "")
	text = strings.ReplaceAll(text, "\r", "")
	return text
}
