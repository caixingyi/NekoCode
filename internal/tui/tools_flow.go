package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/message"
)

// toolResultMsg 是一个工具异步执行完成的结果。
type toolResultMsg struct {
	id      string
	content string
	isError bool
}

// beginToolRound 把首次回复（文本 + tool_use）落为 assistant 消息，进入工具执行/确认队列。
func (m *Model) beginToolRound() tea.Cmd {
	m.sess.Messages = append(m.sess.Messages, message.Message{
		Role:      message.RoleAssistant,
		Content:   m.streamBuf,
		Thinking:  m.thinkBuf,
		Signature: m.signature,
		ToolCalls: m.pendingCalls,
	})
	m.streaming = false
	m.streamBuf = ""
	m.thinkBuf = ""
	m.signature = ""
	m.cancel = nil

	m.confirmQueue = m.pendingCalls
	m.pendingCalls = nil
	m.toolResults = nil
	m.saveSession()
	return m.processNextTool()
}

// processNextTool 处理确认队列的下一个工具：只读自动执行，副作用工具进入确认态。
func (m *Model) processNextTool() tea.Cmd {
	for len(m.confirmQueue) > 0 {
		call := m.confirmQueue[0]
		t, found := m.registry.Get(call.Name)
		if !found {
			m.toolResults = append(m.toolResults, message.ToolResult{ID: call.ID, Content: "未知工具: " + call.Name, IsError: true})
			m.confirmQueue = m.confirmQueue[1:]
			continue
		}
		if t.NeedsConfirm() && !m.allowAll {
			c := call
			m.confirming = &c
			m.confirmText = confirmPreview(call)
			return nil // 等用户按键
		}
		return m.executeToolCmd(call)
	}
	return m.feedResultsAndContinue()
}

// executeToolCmd 异步执行一个工具，结果通过 toolResultMsg 回传。
func (m *Model) executeToolCmd(call message.ToolCall) tea.Cmd {
	m.toolExecuting = true
	reg := m.registry
	return func() tea.Msg {
		res := reg.Execute(context.Background(), call.Name, call.Input)
		content := res.Content
		if content == "" {
			content = "(无输出)"
		}
		return toolResultMsg{id: call.ID, content: content, isError: !res.OK}
	}
}

// onToolResult 记录一个工具结果并推进队列。
func (m *Model) onToolResult(msg toolResultMsg) (tea.Model, tea.Cmd) {
	m.toolExecuting = false
	m.toolResults = append(m.toolResults, message.ToolResult{ID: msg.id, Content: msg.content, IsError: msg.isError})
	if len(m.confirmQueue) > 0 {
		m.confirmQueue = m.confirmQueue[1:]
	}
	return m, m.processNextTool()
}

// confirmResponse 处理用户对当前待确认工具的 y/n/a。
func (m *Model) confirmResponse(r rune) (tea.Model, tea.Cmd) {
	call := *m.confirming
	m.confirming = nil
	m.confirmText = ""
	switch r {
	case 'a':
		m.allowAll = true
		return m, m.executeToolCmd(call)
	case 'y':
		return m, m.executeToolCmd(call)
	default: // n 或其它 = 拒绝
		m.toolResults = append(m.toolResults, message.ToolResult{ID: call.ID, Content: "用户拒绝执行该工具", IsError: true})
		if len(m.confirmQueue) > 0 {
			m.confirmQueue = m.confirmQueue[1:]
		}
		return m, m.processNextTool()
	}
}

// feedResultsAndContinue 把全部工具结果作为 user 消息回灌，发起第二次 stream。
func (m *Model) feedResultsAndContinue() tea.Cmd {
	m.sess.Messages = append(m.sess.Messages, message.Message{
		Role:        message.RoleUser,
		ToolResults: m.toolResults,
	})
	m.toolResults = nil
	m.confirmQueue = nil
	m.saveSession()
	m.inSecondStream = true
	return m.startStream()
}

// busy 表示正在流式/确认/执行工具，此时不接受新输入。
func (m *Model) busy() bool {
	return m.streaming || m.confirming != nil || m.toolExecuting
}

// confirmPreview 生成确认前的预览文本（R11）。
func confirmPreview(call message.ToolCall) string {
	switch call.Name {
	case "run_command":
		return "$ " + argStr(call.Input, "command")
	case "write_file":
		content := argStr(call.Input, "content")
		return "写入 " + argStr(call.Input, "path") + "\n" + previewText(content)
	case "edit_file":
		return "编辑 " + argStr(call.Input, "path") + "\n- " + oneLine(argStr(call.Input, "old_string")) + "\n+ " + oneLine(argStr(call.Input, "new_string"))
	default:
		return fmt.Sprintf("%s %v", call.Name, call.Input)
	}
}

func argStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80]) + "…"
	}
	return s
}

func previewText(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "…")
	}
	return strings.Join(lines, "\n")
}
