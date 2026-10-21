package tui

import (
	"context"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/agent"
	"nekocode/internal/message"
)

// sender 是向 UI 事件循环投递消息的最小接口，*tea.Program 满足它。
// 抽象出来是为了让 TUI 测试能在不启动真实 Program 的情况下驱动确认流程。
type sender interface {
	Send(tea.Msg)
}

// programSender 把消息投给真正的 bubbletea Program（Send 是并发安全的）。
type programSender struct{ p *tea.Program }

func (s programSender) Send(msg tea.Msg) { s.p.Send(msg) }

// confirmRequestMsg 是 agent 从自己的 goroutine 发起的确认请求。
// reply 带回用户的决定；agent 侧阻塞等待，不占用 UI 的事件循环。
type confirmRequestMsg struct {
	call    message.ToolCall
	preview string
	reply   chan agent.Decision
}

// tuiConfirmer 实现 agent.Confirmer：把请求投给 UI，等用户按键。
type tuiConfirmer struct {
	sender   sender
	allowAll *atomic.Bool
}

// Confirm 阻塞等待用户作答，或 ctx 被取消（此时 agent 会走取消收尾路径）。
func (c tuiConfirmer) Confirm(ctx context.Context, call message.ToolCall, preview string) (agent.Decision, error) {
	// 「本会话全部允许」是会话级状态：跨消息、跨循环都生效（R13）。
	if c.allowAll != nil && c.allowAll.Load() {
		return agent.DecisionAllowAll, nil
	}
	reply := make(chan agent.Decision, 1)
	c.sender.Send(confirmRequestMsg{call: call, preview: preview, reply: reply})
	select {
	case d := <-reply:
		return d, nil
	case <-ctx.Done():
		return agent.DecisionDeny, ctx.Err()
	}
}
