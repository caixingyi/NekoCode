package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// markdown 渲染器按换行宽度缓存，宽度变化时重建。
var (
	mdRenderer *glamour.TermRenderer
	mdWrap     int
)

// renderMarkdown 把 markdown 文本渲染成终端 ANSI（暗色主题）。
// 失败时回退为原文；用于已完成的消息（流式中的片段保持纯文本）。
func renderMarkdown(s string, width int) string {
	if strings.TrimSpace(s) == "" {
		return s
	}
	wrap := width - 4
	if wrap < 20 {
		wrap = 20
	}
	if mdRenderer == nil || mdWrap != wrap {
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(wrap),
		)
		if err != nil {
			return s
		}
		mdRenderer, mdWrap = r, wrap
	}
	out, err := mdRenderer.Render(s)
	if err != nil {
		return s
	}
	return strings.Trim(out, "\n")
}
