package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/agent"
	aictx "nekocode/internal/context"
	"nekocode/internal/message"
	"nekocode/internal/provider"
)

// agentEventMsg 包装一条循环事件。
type agentEventMsg agent.Event

// agentClosedMsg 表示循环的事件 channel 已关闭，本次循环结束。
type agentClosedMsg struct{}

// waitForAgentEvent 阻塞读取下一个循环事件，作为 tea.Msg 返回。
func waitForAgentEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentClosedMsg{}
		}
		return agentEventMsg(ev)
	}
}

// nextEvent 保证同一时刻只有一个 goroutine 在读事件 channel。
//
// 这是必须的：确认请求是绕过 event channel 直接投递的，若两处都各自发起读取，
// 就会出现两个读者争抢事件、事件被吞掉的竞态。
func (m *Model) nextEvent() tea.Cmd {
	if m.eventCh == nil || m.waiting {
		return nil
	}
	m.waiting = true
	return waitForAgentEvent(m.eventCh)
}

// startRun 把输入交给 agent 发起一次循环，返回等待首个事件的命令。
func (m *Model) startRun(input string) tea.Cmd {
	prov, err := provider.New(m.profile)
	if err != nil {
		m.errMsg = "创建 provider 失败: " + err.Error()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.waiting = false
	m.turn = 0
	m.stopReason = ""
	m.totalUsage = provider.Usage{}
	m.blocksSeen = 0
	m.executing = 0
	m.lastTool = ""
	m.lastExec = 0
	m.resetTurnBuf()
	m.errMsg = ""
	m.notice = ""

	profile := m.profile
	ag := agent.New(agent.Config{
		Provider:     prov,
		Registry:     m.registry,
		SystemPrompt: profile.EffectiveSystemPrompt(),
		PlanMode:     m.sess.PlanMode,
		Confirmer:    m.confirmer(),
		ContextLimit: func(msgs []message.Message) bool {
			return aictx.ShouldSummarize(profile, msgs)
		},
	})
	m.eventCh = ag.Run(ctx, m.sess.Messages, input)
	return m.nextEvent()
}

// confirmer 构造确认器；没有 UI 通道时返回 nil，agent 会把写类工具按拒绝处理。
func (m *Model) confirmer() agent.Confirmer {
	if m.sender == nil {
		return nil
	}
	return tuiConfirmer{sender: m.sender, allowAll: &m.allowAll}
}

// handleAgentEvent 消费一条循环事件。无论哪种事件都继续等待下一条，
// 直到 channel 关闭（agentClosedMsg）才收尾。
func (m *Model) handleAgentEvent(ev agent.Event) (tea.Model, tea.Cmd) {
	m.waiting = false

	switch ev.Type {
	case agent.EventUserMessage:
		// agent 是历史的唯一真相源，用户消息也由它的快照带回。
		m.syncHistory(ev)

	case agent.EventTextDelta:
		m.turn = ev.Turn
		m.streamBuf += ev.Text

	case agent.EventThinkingDelta:
		m.thinkBuf += ev.Text
		if ev.Signature != "" {
			m.signature = ev.Signature
		}

	case agent.EventToolCallStart:
		m.turn = ev.Turn
		m.executing++
		m.lastTool = ev.ToolCall.Name
		m.lastExec = 0

	case agent.EventToolResult:
		if m.executing > 0 {
			m.executing--
		}
		m.lastExec = ev.Duration

	case agent.EventToolRejected:
		m.notice = "已拒绝该工具，本批剩余写类工具一并跳过"

	case agent.EventToolBlocked:
		m.blocksSeen++
		m.notice = "计划模式已阻止写操作（输入 /plan 退出计划模式）"

	case agent.EventTurnEnd:
		m.turn = ev.Turn
		m.turnUsage = ev.Usage
		m.totalUsage.InputTokens += ev.Usage.InputTokens
		m.totalUsage.OutputTokens += ev.Usage.OutputTokens
		m.syncHistory(ev)
		m.resetTurnBuf()

	case agent.EventDone:
		// Done 的累计用量是权威值，直接覆盖累加结果。
		m.totalUsage = ev.Usage
		m.stopReason = ev.StopReason
		m.syncHistory(ev)

	case agent.EventError:
		m.errMsg = friendlyErr(ev.Err)
		m.syncHistory(ev)
	}

	return m, m.nextEvent()
}

// answerConfirm 把用户的决定回给正在等待的 agent，并恢复读取事件。
func (m *Model) answerConfirm(d agent.Decision) tea.Cmd {
	req := m.confirm
	if req == nil {
		return nil
	}
	m.confirm = nil
	if d == agent.DecisionAllowAll {
		// 会话级开关：后续循环里 tuiConfirmer 会直接放行，不再打扰用户（R13）。
		m.allowAll.Store(true)
	}
	req.reply <- d
	return m.nextEvent()
}

// cancelRun 请求取消当前循环。取消后 agent 仍会把补齐结果的历史快照发回来，
// 所以这里只取消，不直接清理状态。
func (m *Model) cancelRun() {
	if m.cancel != nil {
		m.cancel()
	}
}

// finishRun 在事件 channel 关闭后清理循环状态并给出收场提示。
func (m *Model) finishRun() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.running = false
	m.waiting = false
	m.eventCh = nil
	m.executing = 0
	m.resetTurnBuf()

	if msg := stopReasonNotice(m.stopReason, m.blocksSeen); msg != "" {
		m.notice = msg
	}
}

// syncHistory 用 agent 回带的快照替换本地历史并落盘（R22）。
// 快照只在本轮 assistant 消息与其工具结果都齐了之后才发出，
// 所以每次落盘都是一份 tool_use/tool_result 配对完整的历史。
func (m *Model) syncHistory(ev agent.Event) {
	if len(ev.Messages) == 0 {
		return
	}
	m.sess.Messages = ev.Messages
	m.saveSession()
}

func (m *Model) resetTurnBuf() {
	m.streamBuf = ""
	m.thinkBuf = ""
	m.signature = ""
}

// busy 表示循环进行中或正在等确认，此时不接受新的输入。
func (m *Model) busy() bool {
	return m.running || m.confirm != nil
}

// stopReasonNotice 把循环的收场原因翻译成给用户看的一句话；无需提示时返回空串。
func stopReasonNotice(r agent.StopReason, blocked int) string {
	switch r {
	case agent.StopMaxTurns:
		return "已达本轮上限（25 轮），可直接再发一条消息继续"
	case agent.StopMaxTokens:
		return "模型输出被长度截断而停止，可发「继续」接着做"
	case agent.StopRepeatCall:
		return "检测到重复的工具调用，循环已中止"
	case agent.StopContextLimit:
		return "上下文接近上限，已停止循环；请先 /compact 再继续"
	case agent.StopCanceled:
		return "已取消本次循环（已执行的结果保留在会话里）"
	case agent.StopEmptyResponse:
		return "模型没有返回任何内容"
	case agent.StopEndTurn:
		if blocked > 0 {
			return fmt.Sprintf("本次有 %d 次写操作被计划模式阻止（输入 /plan 退出计划模式）", blocked)
		}
	}
	return ""
}
