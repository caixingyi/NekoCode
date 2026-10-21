package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/agent"
	aictx "nekocode/internal/context"
)

// Update 实现 tea.Model（MVU 中的 Update）。
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(m.width-2, 10))
		return m, nil

	case agentEventMsg:
		return m.handleAgentEvent(agent.Event(msg))

	case agentClosedMsg:
		m.finishRun()
		return m, nil

	case confirmRequestMsg:
		req := msg
		m.confirm = &req
		return m, nil

	case compactDoneMsg:
		return m.handleCompactDone(msg)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleKey 处理按键：确认态只认 y/n/a（Esc 拒绝当前），循环中 Esc/Ctrl+C 取消整个循环。
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, nil
	}
	ks := msg.String()

	if m.confirm != nil {
		switch ks {
		case "y":
			return m, m.answerConfirm(agent.DecisionAllow)
		case "a":
			return m, m.answerConfirm(agent.DecisionAllowAll)
		case "n", "esc":
			// R19：拒绝当前工具，不放整个循环。
			return m, m.answerConfirm(agent.DecisionDeny)
		case "ctrl+c":
			// Ctrl+C 始终是「打断整个循环」，确认态也不例外。
			m.notice = "正在取消..."
			m.cancelRun()
			return m, nil
		}
		return m, nil
	}

	switch ks {
	case "ctrl+c":
		if m.running {
			m.notice = "正在取消..."
			m.cancelRun()
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "esc":
		if m.running {
			m.notice = "正在取消..."
			m.cancelRun()
		}
		return m, nil
	case "enter":
		if !m.busy() {
			return m.handleSubmit()
		}
		return m, nil
	}

	if m.busy() {
		return m, nil // 循环进行中不接受输入
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
		text := m.pendingText
		m.pendingText = ""
		return m, m.startRun(text)
	}
	return m, nil
}

// handleSubmit 处理 Enter 提交：必要时先自动压缩，然后发起一次循环。
//
// 用户消息由 agent 追加进历史并通过快照回带，这里不再自己拼消息——
// agent 是会话历史的唯一真相源。
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

	// R21：达到阈值先自动压缩再发送（循环内部不做压缩）。
	if aictx.ShouldSummarize(m.profile, m.sess.Messages) {
		m.pendingSend = true
		m.pendingText = text
		m.notice = "上下文接近上限，正在自动压缩..."
		return m, m.startCompact()
	}
	return m, m.startRun(text)
}
