package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"nekocode/internal/message"
)

var (
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("36")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
	thinkingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	noticeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
	errStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	summaryStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	catStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	faintStyle     = lipgloss.NewStyle().Faint(true)
	infoStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("36"))
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	toolResStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	confirmStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	planStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
)

// View 实现 tea.Model（MVU 中的 View，纯函数渲染）。
func (m *Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	var b strings.Builder

	if len(m.sess.Messages) == 0 && m.streamBuf == "" && !m.busy() {
		b.WriteString(m.welcome())
	} else {
		b.WriteString(m.header())
		b.WriteString("\n")
		b.WriteString(strings.Repeat("─", max(m.width-2, 20)))
		b.WriteString("\n\n")

		for _, msg := range m.sess.Messages {
			if s := renderMessage(msg, m.width); s != "" {
				b.WriteString(s)
				b.WriteString("\n")
			}
		}

		// 当前轮正在流式生成的内容；已完成的部分由上面的历史快照渲染。
		if m.running && (m.streamBuf != "" || m.thinkBuf != "") {
			if m.thinkBuf != "" {
				b.WriteString(thinkingStyle.Render("💭 " + m.thinkBuf))
				b.WriteString("\n")
			}
			if m.streamBuf != "" {
				b.WriteString(assistantStyle.Render("🤖 " + m.streamBuf))
				b.WriteString("\n")
			}
		}
	}

	// 工具确认提示。
	if m.confirm != nil {
		b.WriteString(toolStyle.Render("🔧 " + m.confirm.call.Name))
		b.WriteString("\n")
		b.WriteString(m.confirm.preview)
		b.WriteString("\n")
		b.WriteString(confirmStyle.Render("执行？ [y] 允许  [n] 拒绝  [a] 本会话全部允许"))
		b.WriteString("\n")
	} else if m.executing > 0 {
		b.WriteString(faintStyle.Render(fmt.Sprintf("⏳ 正在执行 %d 个工具...", m.executing)))
		b.WriteString("\n")
	} else if m.running {
		b.WriteString(faintStyle.Render("⏳ 生成中..."))
		b.WriteString("\n")
	}

	if m.errMsg != "" {
		b.WriteString(errStyle.Render("⚠ " + m.errMsg))
		b.WriteString("\n")
	}
	if m.notice != "" {
		b.WriteString(noticeStyle.Render(m.notice))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if !m.busy() {
		b.WriteString(m.input.View())
	}
	return tea.NewView(b.String())
}

// welcome 渲染启动欢迎界面（空会话时）：猫咪 ASCII + 标题 + 信息 + 开始提示。
func (m *Model) welcome() string {
	cat := catStyle.Render(strings.Join([]string{
		` /\_/\ `,
		`(・ω・)`,
		` つ  つ`,
	}, "\n"))

	rightLines := []string{
		titleStyle.Render("NekoCode") + "  " + faintStyle.Render("v"+Version),
		faintStyle.Render("终端 AI 编程助手"),
		"",
		infoStyle.Render("▸ 当前: ") + m.profileName + " / " + m.profile.Model,
		"",
		faintStyle.Render("开始:"),
		"  • 输入问题，Enter 发送",
		"  • /help 查看命令 · /switch 换模型",
		"  • /think 开启思考(Claude) · /plan 计划模式",
		"  • Ctrl+J 换行 · Esc/Ctrl+C 取消 · Ctrl+C 退出",
	}
	right := strings.Join(rightLines, "\n")

	banner := lipgloss.JoinHorizontal(lipgloss.Top, cat, "  ", right)
	return banner + "\n" + strings.Repeat("─", max(m.width-2, 20)) + "\n\n"
}

// header 渲染对话中的紧凑顶栏，并在循环进行时显示进度信息（R3）。
func (m *Model) header() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("NekoCode"))
	b.WriteString(" ")
	b.WriteString(m.profileName)
	b.WriteString("/")
	b.WriteString(m.profile.Model)
	if m.sess.PlanMode {
		b.WriteString("  ")
		b.WriteString(planStyle.Render("[PLAN]"))
	}
	if m.running && m.turn > 0 {
		b.WriteString("  ")
		b.WriteString(faintStyle.Render(fmt.Sprintf("第 %d 轮", m.turn)))
	}
	if m.lastExec > 0 {
		b.WriteString("  ")
		b.WriteString(faintStyle.Render(fmt.Sprintf("%s %s", m.lastTool, m.lastExec.Round(time.Millisecond))))
	}
	if u := m.totalUsage; u.InputTokens+u.OutputTokens > 0 {
		b.WriteString("  ")
		b.WriteString(faintStyle.Render(fmt.Sprintf("↑%d ↓%d tok", u.InputTokens, u.OutputTokens)))
	}
	b.WriteString("  ")
	b.WriteString(faintStyle.Render("/help · Esc 取消 · Ctrl+C 退出"))
	return b.String()
}

func renderMessage(msg message.Message, width int) string {
	switch msg.Role {
	case message.RoleUser:
		if len(msg.ToolResults) > 0 {
			var s strings.Builder
			for _, tr := range msg.ToolResults {
				marker := "✓"
				st := toolResStyle
				if tr.IsError {
					marker, st = "✗", errStyle
				}
				s.WriteString(st.Render("  " + marker + " "))
				s.WriteString(faintStyle.Render(oneLine(tr.Content)))
				s.WriteString("\n")
			}
			return strings.TrimRight(s.String(), "\n")
		}
		return userStyle.Render("你: ") + msg.Content
	case message.RoleAssistant:
		var s strings.Builder
		if msg.Thinking != "" {
			s.WriteString(thinkingStyle.Render("💭 " + msg.Thinking))
			s.WriteString("\n")
		}
		if strings.TrimSpace(msg.Content) != "" {
			s.WriteString(assistantStyle.Render("🤖"))
			s.WriteString("\n")
			s.WriteString(renderMarkdown(msg.Content, width))
			s.WriteString("\n")
		}
		for _, tc := range msg.ToolCalls {
			s.WriteString(toolStyle.Render("🔧 " + tc.Name + " " + oneLine(argsSummary(tc.Input))))
			s.WriteString("\n")
		}
		return strings.TrimRight(s.String(), "\n")
	case message.RoleSystem:
		if msg.IsSummary {
			return summaryStyle.Render("[摘要] " + msg.Content)
		}
		return ""
	}
	return msg.Content
}

// argsSummary 把工具参数压成一行摘要。
func argsSummary(input map[string]any) string {
	if p, ok := input["path"].(string); ok {
		return p
	}
	if c, ok := input["command"].(string); ok {
		return c
	}
	if p, ok := input["pattern"].(string); ok {
		return p
	}
	return ""
}

// oneLine 把多行文本压成单行并截断，用于工具结果摘要。
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80]) + "…"
	}
	return s
}
