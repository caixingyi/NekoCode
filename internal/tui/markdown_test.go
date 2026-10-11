package tui

import (
	"regexp"
	"strings"
	"testing"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI 去掉终端 ANSI 转义，方便断言渲染后的纯文本内容。
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func TestRenderMarkdownBasic(t *testing.T) {
	out := stripANSI(renderMarkdown("# 标题\n\n- 列表项\n\n`行内代码`", 80))
	for _, want := range []string{"标题", "列表项", "行内代码"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderMarkdown missing %q: %q", want, out)
		}
	}
}

func TestRenderMarkdownCodeBlock(t *testing.T) {
	out := stripANSI(renderMarkdown("```go\nfmt.Println(1)\n```", 80))
	if !strings.Contains(out, "fmt.Println(1)") {
		t.Errorf("code block missing content: %q", out)
	}
}

func TestRenderMarkdownEmptyPassthrough(t *testing.T) {
	if out := renderMarkdown("", 80); out != "" {
		t.Errorf("empty input should stay empty, got %q", out)
	}
	if out := renderMarkdown("   \n  ", 80); out != "   \n  " {
		t.Errorf("whitespace input should pass through unchanged, got %q", out)
	}
}

func TestRenderMarkdownNarrowWidthNoPanic(t *testing.T) {
	out := stripANSI(renderMarkdown("这是一段很长的中文内容，用来测试在非常窄的终端宽度下折行是否正常，不会崩溃。", 10))
	if strings.TrimSpace(out) == "" {
		t.Errorf("narrow width should still render content, got %q", out)
	}
}
