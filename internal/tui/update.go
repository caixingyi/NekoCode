package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	aictx "nekocode/internal/context"
	"nekocode/internal/message"
	"nekocode/internal/provider"
)

// Update 实现 tea.Model（MVU 中的 Update）。
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(m.width-2, 10))
		return m, nil

	case streamEventMsg:
		return m.handleStreamEvent(provider.StreamEvent(msg))

	case streamClosedMsg:
		if !m.inSecondStream && len(m.pendingCalls) > 0 {
			return m, m.beginToolRound()
		}
		m.finalizeStream()
		return m, nil

	case toolResultMsg:
		return m.onToolResult(msg)

	case compactDoneMsg:
		return m.handleCompactDone(msg)

	case tea.KeyPressMsg:
		if m.quitting {
			return m, nil
		}
		ks := msg.String()
		// 确认态：只认 y/n/a（esc/ctrl+c 视为拒绝当前）。
		if m.confirming != nil {
			switch ks {
			case "y":
				return m.confirmResponse('y')
			case "a":
				return m.confirmResponse('a')
			case "n", "esc", "ctrl+c":
				return m.confirmResponse('n')
			}
			return m, nil
		}
		switch ks {
		case "ctrl+c":
			if m.streaming && m.cancel != nil {
				m.cancel()
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if !m.busy() {
				return m.handleSubmit()
			}
			return m, nil
		}
		if m.busy() {
			return m, nil // 流式/工具执行中不接受输入
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleCompactDone 处理异步摘要压缩结果；若为自动压缩后待发送则继续发送。
func (m *Model) handleCompactDone(msg compactDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "压缩失败: " + msg.err.Error()
	} else if msg.msgs != nil {
		m.sess.Messages = msg.msgs
		m.saveSession()
		m.notice = fmt.Sprintf("已压缩 %d 轮旧消息", msg.n)
	}
	if m.pendingSend {
		m.pendingSend = false
		return m, m.startStream()
	}
	return m, nil
}

// handleSubmit 处理 Enter 提交：加入用户消息、必要时自动压缩、发起流式。
func (m *Model) handleSubmit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		return m.handleCommand(text)
	}
	m.input.Reset()
	m.errMsg = ""
	m.notice = ""
	m.sess.Messages = append(m.sess.Messages, message.NewMessage(message.RoleUser, text))

	// R23：达到阈值先自动压缩再发送。
	if aictx.ShouldSummarize(m.profile, m.sess.Messages) {
		m.pendingSend = true
		m.notice = "上下文接近上限，正在自动压缩..."
		return m, m.startCompact()
	}
	return m, m.startStream()
}
