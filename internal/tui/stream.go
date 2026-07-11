package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	aictx "nekocode/internal/context"
	"nekocode/internal/message"
	"nekocode/internal/provider"
)

// streamEventMsg 包装一条 provider 流式事件。
type streamEventMsg provider.StreamEvent

// streamClosedMsg 表示 stream channel 已关闭。
type streamClosedMsg struct{}

// compactDoneMsg 是异步摘要压缩的结果。
type compactDoneMsg struct {
	msgs []message.Message
	n    int
	err  error
}

// waitForEvent 阻塞读取 stream channel 的下一个事件，作为 tea.Msg 返回。
func waitForEvent(ch <-chan provider.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return streamEventMsg(ev)
	}
}

// startStream 发起一轮流式对话，返回首个等待命令。
func (m *Model) startStream() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true
	m.streamBuf = ""
	m.thinkBuf = ""
	m.signature = ""
	m.errMsg = ""
	m.notice = ""

	prov, err := provider.New(m.profile)
	if err != nil {
		m.streaming = false
		m.errMsg = "创建 provider 失败: " + err.Error()
		return nil
	}
	m.streamCh = prov.Stream(ctx, m.profile.EffectiveSystemPrompt(), m.sess.Messages)
	return waitForEvent(m.streamCh)
}

// handleStreamEvent 处理一条流式事件，返回下一个等待命令。
func (m *Model) handleStreamEvent(ev provider.StreamEvent) (tea.Model, tea.Cmd) {
	switch ev.Type {
	case provider.EventText:
		m.streamBuf += ev.Text
		return m, waitForEvent(m.streamCh)
	case provider.EventThinking:
		if ev.Text != "" {
			m.thinkBuf += ev.Text
		}
		if ev.Signature != "" {
			m.signature = ev.Signature
		}
		return m, waitForEvent(m.streamCh)
	case provider.EventDone:
		m.finalizeStream()
		return m, nil
	case provider.EventError:
		m.errMsg = friendlyErr(ev.Err)
		m.finalizeStream() // 保留已生成文本（R26）
		return m, nil
	}
	return m, waitForEvent(m.streamCh)
}

// finalizeStream 结束一轮流式：把已生成文本落为 assistant 消息、落盘、清理状态。
func (m *Model) finalizeStream() {
	if !m.streaming {
		return
	}
	if m.streamBuf != "" {
		m.sess.Messages = append(m.sess.Messages, message.Message{
			Role:      message.RoleAssistant,
			Content:   m.streamBuf,
			Thinking:  m.thinkBuf,
			Signature: m.signature,
		})
	}
	m.streaming = false
	m.streamBuf = ""
	m.thinkBuf = ""
	m.signature = ""
	m.cancel = nil
	m.saveSession() // R21：每轮结束自动落盘
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
