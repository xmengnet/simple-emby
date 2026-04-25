package danmaku

import (
	"fmt"
	"regexp"
	"strings"
)

// 用于过滤影响渲染性能的特殊字符。
// 使用 Unicode 类别进行更全面的过滤：
// \p{So} (Symbol, Other): 包含了 Emoji、方块、几何图形、盲文、杂项符号等绝大多数引发 fallback 的字符。
// \p{C} (Other, Control): 包含了不可见的控制字符、私有区字符等。
var blockCharRegex = regexp.MustCompile(`[\p{So}\p{C}]`)

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
	// 剔除容易引起 mpv(libass) 卡顿和风扇狂转的特殊形状/Emoji字符
	text = blockCharRegex.ReplaceAllString(text, "")
	return text
}
