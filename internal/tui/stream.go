package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	aictx "nekocode/internal/context"
	"nekocode/internal/message"
	"nekocode/internal/provider"
)

// compactDoneMsg 是异步摘要压缩的结果。
type compactDoneMsg struct {
	msgs []message.Message
	n    int
	err  error
}

// startCompact 异步触发摘要压缩（force=true），结果通过 compactDoneMsg 回传。
func (m *Model) startCompact() tea.Cmd {
	profile := m.profile
	msgs := make([]message.Message, len(m.sess.Messages))
	copy(msgs, m.sess.Messages)
	return func() tea.Msg {
		prov, err := provider.New(profile)
		if err != nil {
			return compactDoneMsg{err: err}
		}
		out, n, err := aictx.Summarize(context.Background(), prov, profile, msgs, true)
		return compactDoneMsg{msgs: out, n: n, err: err}
	}
}

// saveSession 估算 token 并落盘（R21）。失败仅提示，不中断。
func (m *Model) saveSession() {
	if m.store == nil {
		return
	}
	m.sess.EstTokens = aictx.Estimate(m.profile.EffectiveSystemPrompt(), m.sess.Messages)
	if err := m.store.Save(m.sess); err != nil {
		m.notice = "保存会话失败: " + err.Error()
	}
}

// friendlyErr 把流式/HTTP 错误映射为可读提示（R27）。
func friendlyErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "401"), strings.Contains(s, "403"):
		return "鉴权失败：api_key 或 base_url 可能有误（" + s + "）"
	case strings.Contains(s, "429"):
		return "请求过于频繁，请稍后重试（429 限流）"
	default:
		return s
	}
}
