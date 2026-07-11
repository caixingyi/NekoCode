package tui

import (
	"strings"

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
)

// View 实现 tea.Model（MVU 中的 View，纯函数渲染）。
func (m *Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	var b strings.Builder

	if len(m.sess.Messages) == 0 && m.streamBuf == "" {
		b.WriteString(m.welcome())
	} else {
		b.WriteString(m.header())
		b.WriteString("\n")
		b.WriteString(strings.Repeat("─", max(m.width-2, 20)))
		b.WriteString("\n\n")

		for _, msg := range m.sess.Messages {
			if s := renderMessage(msg); s != "" {
				b.WriteString(s)
				b.WriteString("\n")
			}
		}

		// 流式中的回复（含 thinking 区块）。
		if m.streaming && (m.streamBuf != "" || m.thinkBuf != "") {
			if m.thinkBuf != "" {
				b.WriteString(thinkingStyle.Render("💭 "+m.thinkBuf))
				b.WriteString("\n")
			}
			if m.streamBuf != "" {
				b.WriteString(assistantStyle.Render("🤖 "+m.streamBuf))
				b.WriteString("\n")
			}
		}
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
	b.WriteString(m.input.View())
	return tea.NewView(b.String())
}

// welcome 渲染启动欢迎界面（空会话时）：猫咪 ASCII + 标题 + 信息 + 开始提示。
func (m *Model) welcome() string {
	cat := catStyle.Render(strings.Join([]string{
		` /\_/\ `,
		`( o.o )`,
		` > ^ < `,
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
		"  • /think 开启思考(Claude) · /resume 恢复会话",
		"  • Ctrl+J 换行 · Ctrl+C 退出",
	}
	right := strings.Join(rightLines, "\n")

	banner := lipgloss.JoinHorizontal(lipgloss.Top, cat, "  ", right)
	return banner + "\n" + strings.Repeat("─", max(m.width-2, 20)) + "\n\n"
}

// header 渲染对话中的紧凑顶栏。
func (m *Model) header() string {
	return titleStyle.Render("NekoCode") + " " + m.profileName + "/" + m.profile.Model +
		"  " + faintStyle.Render("/help · Ctrl+C 退出")
}

func renderMessage(msg message.Message) string {
	switch msg.Role {
	case message.RoleUser:
		return userStyle.Render("你: ") + msg.Content
	case message.RoleAssistant:
		var s strings.Builder
		if msg.Thinking != "" {
			s.WriteString(thinkingStyle.Render("💭 "+msg.Thinking))
			s.WriteString("\n")
		}
		s.WriteString(assistantStyle.Render("🤖 "))
		s.WriteString(msg.Content)
		return s.String()
	case message.RoleSystem:
		if msg.IsSummary {
			return summaryStyle.Render("[摘要] " + msg.Content)
		}
		return ""
	}
	return msg.Content
}
