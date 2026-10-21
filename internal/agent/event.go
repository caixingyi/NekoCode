// Package agent 实现 NekoCode 的 ReAct 循环：调用方传入历史与用户输入，
// 它以事件流的形式暴露「调用模型 → 执行工具 → 回填结果 → 再调用」的全过程。
//
// 本包不做文件 IO、不依赖 TUI：会话落盘由消费方根据事件里的历史快照完成，
// 界面交互通过 Confirmer 接口回调。这样它既能被 TUI 消费，也能被测试或无头场景复用。
package agent

import (
	"time"

	"nekocode/internal/message"
	"nekocode/internal/provider"
)

// StopReason 是循环的收场原因（R5–R9、R21）。
type StopReason string

const (
	// StopEndTurn 模型正常结束（停止原因为 end_turn，或本轮没有工具调用）。
	StopEndTurn StopReason = "end_turn"
	// StopEmptyResponse 模型既没输出文本也没请求工具（允许只有 thinking）。
	StopEmptyResponse StopReason = "empty_response"
	// StopMaxTokens 模型输出被长度截断。
	StopMaxTokens StopReason = "max_tokens"
	// StopMaxTurns 达到轮数上限。
	StopMaxTurns StopReason = "max_turns"
	// StopRepeatCall 连续多次出现完全相同的工具调用，判定为卡住。
	StopRepeatCall StopReason = "repeat_call"
	// StopContextLimit 上下文接近上限，需用户先压缩再继续。
	StopContextLimit StopReason = "context_limit"
	// StopCanceled 用户取消。
	StopCanceled StopReason = "canceled"
	// StopError 上游错误（网络/API）。
	StopError StopReason = "error"
)

// EventType 标识循环事件的种类（R2）。
type EventType int

const (
	// EventUserMessage 循环开始，Text 为本轮用户输入。
	EventUserMessage EventType = iota
	// EventThinkingDelta 思考增量；Signature 非空表示 thinking 块结束。
	EventThinkingDelta
	// EventTextDelta 回答文本增量。
	EventTextDelta
	// EventToolCallStart 一个工具即将执行（已通过确认）。
	EventToolCallStart
	// EventToolResult 一个工具执行完成。
	EventToolResult
	// EventToolRejected 用户拒绝了一个写类工具（TUI 据此可提示「本批剩余写类已跳过」）。
	EventToolRejected
	// EventToolBlocked plan 模式下写类工具被拦截（R15）。
	EventToolBlocked
	// EventTurnEnd 一轮结束，Messages 为本轮提交后的完整历史快照。
	EventTurnEnd
	// EventDone 整个循环结束。
	EventDone
	// EventError 上游错误。
	EventError
)

// Event 是循环对外暴露的一条事件（R2、R3），这里的对外就是指的对TUI
type Event struct {
	Type EventType
	// Turn 是轮次序号，从 1 开始；EventUserMessage 为 0。
	Turn int
	// Batch 是轮内批次号：同一批工具共享一个号（读批与写批各一个）。
	Batch        int
	Text         string
	Signature    string
	ToolCall     message.ToolCall
	Result       message.ToolResult
	NeedsConfirm bool
	// Duration 是单个工具的执行耗时（仅 EventToolResult）。
	Duration time.Duration
	// Usage 是 token 用量：EventTurnEnd 为本轮，EventDone 为累计。
	Usage provider.Usage
	// StopReason：EventTurnEnd 为模型本轮的停止原因，EventDone 为循环的收场原因。
	StopReason StopReason
	// Messages 是提交后的完整历史快照，消费方据此落盘（R22）。
	Messages []message.Message
	Err      error
}
