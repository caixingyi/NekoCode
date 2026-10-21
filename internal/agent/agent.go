package agent

import (
	"context"
	"strings"
	"time"

	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/tool"
)

// 默认参数（除轮数上限与并发上限外，重复阈值也在 spec 中确认）。
const (
	defaultMaxTurns    = 25
	defaultMaxParallel = 8
	defaultRepeatLimit = 3
	// eventBuffer 是事件 channel 的缓冲；消费方必须一直读到 channel 关闭。
	eventBuffer = 16
)

// Config 是构造 Agent 的全部输入。
type Config struct {
	// Provider 与 Registry 为必需项；Registry 为 nil 时所有工具调用都返回「未知工具」。
	Provider provider.Provider
	Registry *tool.Registry
	// SystemPrompt 直接透传给 provider。
	SystemPrompt string
	// PlanMode 为真时拦截写类工具（R15）。
	PlanMode bool
	// MaxTurns 为轮数上限，默认 25。
	MaxTurns int
	// MaxParallel 为并发读的上限，默认 8。
	MaxParallel int
	// RepeatLimit 为「连续多少次相同调用算卡住」，默认 3。
	RepeatLimit int
	// Confirmer 用于写类工具的确认；为 nil 时写类一律按拒绝处理（安全默认）。
	Confirmer Confirmer
	// ContextLimit 在每轮结束时被调用，返回 true 则中止循环（R21）；为 nil 表示不检查。
	ContextLimit func([]message.Message) bool
}

// Agent 是无状态的循环配置载体：同一实例可被多次 Run，也可并发使用。
type Agent struct {
	cfg Config
}

// New 构造 Agent，并把未设置的数值参数补成默认值。
func New(cfg Config) *Agent {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = defaultMaxTurns
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = defaultMaxParallel
	}
	// 阈值 1 会让任何一次工具调用都算「重复」，没有意义，按默认值兜底。
	if cfg.RepeatLimit < 2 {
		cfg.RepeatLimit = defaultRepeatLimit
	}
	return &Agent{cfg: cfg}
}

// Run 启动一次循环：把 input 追加到 history 之后开始迭代，事件通过返回的 channel 给出。
// channel 在循环结束（含取消/出错）后关闭。
//
// 消费方必须一直读到 channel 关闭：取消与出错时循环仍需发出「补齐工具结果」后的
// 最终历史快照，读端提前放弃会让发送方永久阻塞。
func (a *Agent) Run(ctx context.Context, history []message.Message, input string) <-chan Event {
	ch := make(chan Event, eventBuffer)
	r := &run{
		agent:    a,
		ctx:      ctx,
		send:     func(ev Event) { ch <- ev },
		detector: newRepeatDetector(a.cfg.RepeatLimit),
	}
	go func() {
		defer close(ch)
		r.loop(history, input)
	}()
	return ch
}

// emit 是事件发送函数。发送是阻塞的，理由见 Run 的说明。
type emit func(Event)

// run 持有单次循环的可变状态，保证 Agent 本身可复用。
type run struct {
	agent    *Agent
	ctx      context.Context
	send     emit
	detector *repeatDetector
	allowAll bool // 用户选择「本会话全部允许」后为真（R13）
}

// cloneMessages 复制消息切片，作为历史快照发给消费方（消息本身不被就地修改）。
func cloneMessages(msgs []message.Message) []message.Message {
	return append([]message.Message(nil), msgs...)
}

// commitTurn 把一轮的 assistant 消息与其后的工具结果作为一个原子单元追加进历史。
//
// 这是「落盘边界永远合法」的关键：assistant 里的 tool_use 与紧随的 user 消息里的
// tool_result 同生共死，中途被杀也不会留下悬空的 tool_use（R22）。
func commitTurn(msgs []message.Message, asst message.Message, results []message.ToolResult) []message.Message {
	hasAssistant := strings.TrimSpace(asst.Content) != "" ||
		strings.TrimSpace(asst.Thinking) != "" ||
		len(asst.ToolCalls) > 0
	if hasAssistant {
		msgs = append(msgs, asst)
	}
	if len(results) > 0 {
		msgs = append(msgs, message.Message{
			Role:        message.RoleUser,
			ToolResults: results,
			CreatedAt:   time.Now(),
		})
	}
	return msgs
}

// synthesizeResults 为一批没有真正执行的工具调用合成失败结果，
// 目的是让每个 tool_use 都有配对的 tool_result。
// 保证满足 Anthropic 的协议要求
func synthesizeResults(calls []message.ToolCall, reason string) []message.ToolResult {
	if len(calls) == 0 {
		return nil
	}
	out := make([]message.ToolResult, 0, len(calls))
	for _, c := range calls {
		out = append(out, message.ToolResult{ID: c.ID, Content: reason, IsError: true})
	}
	return out
}
