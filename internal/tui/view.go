package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"nekocode/internal/message"
	"nekocode/internal/provider"
)

var (
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("36")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
	thinkingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	noticeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
	errStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
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
		if m.errMsg != "" {
			b.WriteString(errStyle.Render("⚠ " + m.errMsg))
			b.WriteString("\n")
		}
		if m.notice != "" {
			b.WriteString(noticeStyle.Render(m.notice))
			b.WriteString("\n")
		}
		if m.errMsg != "" || m.notice != "" {
			b.WriteString("\n")
		}
		b.WriteString(m.input.View())
		v := tea.NewView(b.String())
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	// 固定顶栏 + 可滚动历史 + 固定底部（状态/确认/输入）。
	bottom := m.bottomView()
	m.syncViewport(bottom)
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.contentWidth()))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimRight(m.viewport.View(), "\n"))
	b.WriteString("\n")
	b.WriteString(bottom)
	v := tea.NewView(b.String())
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// contentWidth 是历史/分隔线使用的宽度；留 2 列给终端边距。
func (m *Model) contentWidth() int {
	return max(m.width-2, 20)
}

// syncViewport 把当前历史与流式内容同步到 viewport，并按底部高度计算可视行数。
// 用户没有向上翻时自动跟随尾部；向上翻后保留原偏移，不被新内容拽回。
func (m *Model) syncViewport(bottom string) {
	m.viewport.SetWidth(m.contentWidth())
	h := 20
	if m.height > 0 {
		h = m.height - 3 - lipgloss.Height(bottom)
		if h < 3 {
			h = 3
		}
	}
	m.viewport.SetHeight(h)

	wasBottom := m.viewport.AtBottom()
	y := m.viewport.YOffset()
	m.viewport.SetContent(m.conversationView())
	if m.followTail || wasBottom {
		m.viewport.GotoBottom()
		m.followTail = true
		return
	}
	m.viewport.SetYOffset(y)
}

// conversationView 渲染历史 + 当前流式内容，不包含顶栏和底部输入区。
func (m *Model) conversationView() string {
	var b strings.Builder
	width := m.contentWidth()
	for _, msg := range m.sess.Messages {
		if s := renderMessage(msg, width); s != "" {
			b.WriteString(s)
			b.WriteString("\n")
		}
	}
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
	return strings.TrimRight(b.String(), "\n")
}

// bottomView 是固定在视口下方的状态、确认提示与输入框。
func (m *Model) bottomView() string {
	var b strings.Builder
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
	return strings.TrimRight(b.String(), "\n")
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
		"  • 空输入 ↑/↓ 滚动历史",
		"  • PgUp/PgDn 或 Alt+↑↓ 翻页 · Ctrl+J 换行",
		"  • Esc/Ctrl+C 取消 · Ctrl+C 退出",
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
	// 窄终端按优先级降级：先省累计 token 与帮助提示，缓存命中始终保留。
	detail := m.width == 0 || m.width >= 100
	if detail {
		if u := m.totalUsage; u.InputTokens+u.OutputTokens > 0 {
			b.WriteString("  ")
			b.WriteString(faintStyle.Render(fmt.Sprintf("↑%d ↓%d tok", u.InputTokens, u.OutputTokens)))
		}
	}
	b.WriteString("  ")
	if m.width > 0 && m.width < 100 {
		b.WriteString(faintStyle.Render(compactCacheText(m.turnUsage)))
	} else {
		b.WriteString(faintStyle.Render(cacheText(m.turnUsage)))
	}
	if detail {
		b.WriteString("  ")
		b.WriteString(faintStyle.Render("/help · Esc 取消 · Ctrl+C 退出"))
	}
	return b.String()
}

// cacheText 展示最近一轮的缓存读取/总输入/命中率；未知时显示 —。
func cacheText(u provider.Usage) string {
	if !u.CacheUsageKnown || u.InputTokens <= 0 {
		return "缓存 —"
	}
	rate := float64(u.CacheReadTokens) / float64(u.InputTokens) * 100
	if u.CacheReadTokens == 0 && u.CacheCreationTokens > 0 {
		return fmt.Sprintf("缓存 0%% (写 %s)", formatTokens(u.CacheCreationTokens))
	}
	return fmt.Sprintf("缓存 %s/%s %.0f%%", formatTokens(u.CacheReadTokens), formatTokens(u.InputTokens), rate)
}

// compactCacheText 是窄终端下的短版本，只保留命中率。
func compactCacheText(u provider.Usage) string {
	if !u.CacheUsageKnown || u.InputTokens <= 0 {
		return "缓存 —"
	}
	rate := float64(u.CacheReadTokens) / float64(u.InputTokens) * 100
	return fmt.Sprintf("缓存 %.0f%%", rate)
}

func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func renderMessage(msg message.Message, width int) string {
	if msg.IsSupplement {
		return ""
	}
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
