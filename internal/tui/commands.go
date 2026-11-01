package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/prompt"
	"nekocode/internal/session"
)

// handleCommand 处理 slash 命令（R15）。
func (m *Model) handleCommand(text string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.errMsg = ""
	m.notice = ""
	parts := strings.Fields(text)
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}
	switch cmd {
	case "/quit", "/exit":
		m.saveSession()
		m.quitting = true
		return m, tea.Quit
	case "/help":
		m.notice = "命令: /help /quit /clear /new /resume [n] /switch [profile] /think /plan /compact · 空输入 ↑/↓、PgUp/PgDn 或 Alt+↑/↓ 滚动历史"
		return m, nil
	case "/clear":
		m.sess.Messages = nil
		m.envNeedsRefresh = true
		m.followTail = true
		m.notice = "已清空当前会话上下文"
		m.saveSession()
		return m, nil
	case "/new":
		m.saveSession()
		m.sess = session.NewSession(m.profileName)
		m.envNeedsRefresh = true
		m.followTail = true
		m.notice = "已新建会话"
		return m, nil
	case "/resume":
		return m.handleResume(arg)
	case "/switch":
		return m.handleSwitch(arg)
	case "/think":
		return m.handleThink()
	case "/plan":
		return m.handlePlan()
	case "/compact":
		m.notice = "正在压缩..."
		return m, m.startCompact()
	default:
		m.errMsg = "未知命令: " + text
		return m, nil
	}
}

// handleResume 列出或恢复历史会话（R18）。
func (m *Model) handleResume(arg string) (tea.Model, tea.Cmd) {
	list, err := m.store.List(0)
	if err != nil {
		m.errMsg = "读取会话列表失败: " + err.Error()
		return m, nil
	}
	if arg == "" {
		if len(list) == 0 {
			m.notice = "没有历史会话"
			return m, nil
		}
		var b strings.Builder
		b.WriteString("历史会话（/resume <序号> 恢复）:")
		for i, s := range list {
			b.WriteString(fmt.Sprintf("\n%d. %s  [%s]  %s", i+1, s.DisplayTitle(), s.CurrentProfile, s.UpdatedAt.Format("01-02 15:04")))
		}
		m.notice = b.String()
		return m, nil
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 || n > len(list) {
		m.errMsg = "无效序号: " + arg
		return m, nil
	}
	m.saveSession()
	sess, err := m.store.Load(list[n-1].ID)
	if err != nil {
		m.errMsg = "恢复会话失败: " + err.Error()
		return m, nil
	}
	m.sess = sess
	// 恢复时一并切换到该会话记录的 profile，并重建 instructions。
	if p, perr := m.cfg.Profile(m.sess.CurrentProfile); perr == nil {
		m.profile = p
		m.profileName = m.sess.CurrentProfile
		m.instructions = prompt.New(p.SystemPrompt)
	}
	m.envNeedsRefresh = true
	m.followTail = true
	m.notice = "已恢复会话: " + m.sess.DisplayTitle()
	return m, nil
}

// handleSwitch 切换 provider profile，携带历史（R20）。
func (m *Model) handleSwitch(arg string) (tea.Model, tea.Cmd) {
	if arg == "" {
		names := make([]string, 0, len(m.cfg.Profiles))
		for n := range m.cfg.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		m.notice = "可用 profile: " + strings.Join(names, ", ") + "（/switch <name>）"
		return m, nil
	}
	p, err := m.cfg.Profile(arg)
	if err != nil {
		m.errMsg = "切换失败: " + err.Error()
		return m, nil
	}
	m.profile = p
	m.profileName = arg
	m.instructions = prompt.New(p.SystemPrompt)
	m.sess.RecordSwitch(arg)
	m.saveSession()
	m.notice = "已切换到 " + arg + "（历史已携带）"
	return m, nil
}

// handlePlan 切换计划模式（R14）。开关随会话落盘，/resume 一并恢复。
func (m *Model) handlePlan() (tea.Model, tea.Cmd) {
	m.sess.PlanMode = !m.sess.PlanMode
	instructions := m.instructions
	if instructions == nil {
		instructions = prompt.New("")
	}
	var body string
	if m.sess.PlanMode {
		body = instructions.EnterPlanSupplement()
		m.notice = "已进入计划模式：只读工具可用，写类工具会被拦截；再输入 /plan 退出"
	} else {
		body = instructions.ExitPlanSupplement()
		m.notice = "已退出计划模式，写操作恢复正常"
	}
	seq := message.NextSupplementSeq(m.sess.Messages)
	m.sess.Messages = append(m.sess.Messages, message.NewModeSupplement(seq, m.sess.PlanMode, true, body))
	m.saveSession()
	return m, nil
}

// handleThink 临时开关 extended thinking（仅 Claude，R10/R15）。
func (m *Model) handleThink() (tea.Model, tea.Cmd) {
	if m.profile.Protocol != config.ProtocolAnthropic {
		m.notice = "thinking 仅 Claude 支持（当前 protocol: " + m.profile.Protocol + "）"
		return m, nil
	}
	enabled := false
	budget := 2048
	if m.profile.Thinking != nil {
		enabled = m.profile.Thinking.Enabled
		budget = m.profile.Thinking.BudgetTokens
	}
	if budget < config.MinThinkingBudget {
		budget = 2048
	}
	// 新建 ThinkingConfig，避免修改 cfg 中的指针共享状态。
	m.profile.Thinking = &config.ThinkingConfig{Enabled: !enabled, BudgetTokens: budget}
	if m.profile.Thinking.Enabled {
		m.notice = "已开启 thinking（budget=" + strconv.Itoa(budget) + "）"
	} else {
		m.notice = "已关闭 thinking"
	}
	return m, nil
}
