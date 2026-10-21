// Package provider 定义 LLM Provider 的统一流式对话接口，
// 并内置 Anthropic 与 OpenAI 两种实现，共享 SSE 读取器。
package provider

import (
	"context"
	"fmt"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/tool"
)

// EventType 标识流式事件的种类。
type EventType int

const (
	// EventText 是模型回答的文本增量。
	EventText EventType = iota
	// EventThinking 是 Claude extended thinking 的思考增量。
	EventThinking
	// EventToolCall 表示模型请求调用一个工具（参数拼接完成后发出）。
	EventToolCall
	// EventDone 表示流式生成正常结束。
	EventDone
	// EventError 表示流式过程中发生错误（网络/API/SSE error）。
	EventError
)

// 归一化后的停止原因（R24）：两家的取值统一到这三个，供 agent 判定如何收场。
const (
	// StopEndTurn 表示模型正常结束（Anthropic end_turn/stop_sequence、OpenAI stop 等）。
	StopEndTurn = "end_turn"
	// StopToolUse 表示模型请求调用工具（Anthropic tool_use、OpenAI tool_calls）。
	StopToolUse = "tool_use"
	// StopMaxTokens 表示输出被长度截断（Anthropic max_tokens、OpenAI length）。
	StopMaxTokens = "max_tokens"
)

// Usage 是一次模型调用的 token 用量（R25）。
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// StreamEvent 是 Provider 流式输出的统一事件。
//
// 对于 EventThinking：Text 为思考文本增量；thinking block 结束时发一条 Text 空、
// Signature 非空的事件。EventToolCall 时 ToolCall 携带完整调用（含拼接好的参数）。
// StopReason 与 Usage 随 EventDone 给出，服务端未返回时分别为空串与 nil。
type StreamEvent struct {
	Type       EventType
	Text       string
	Err        error
	Signature  string
	ToolCall   message.ToolCall
	StopReason string
	Usage      *Usage
}

// normalizeAnthropicStop 把 Anthropic 的 stop_reason 归一化为统一取值。
func normalizeAnthropicStop(r string) string {
	switch r {
	case "":
		return ""
	case StopMaxTokens:
		return StopMaxTokens
	case StopToolUse:
		return StopToolUse
	default: // end_turn / stop_sequence / 未知值
		return StopEndTurn
	}
}

// normalizeOpenAIStop 把 OpenAI 的 finish_reason 归一化为统一取值。
func normalizeOpenAIStop(r string) string {
	switch r {
	case "":
		return ""
	case "length":
		return StopMaxTokens
	case "tool_calls", "function_call":
		return StopToolUse
	default: // stop / content_filter / 未知值
		return StopEndTurn
	}
}

// Provider 是 LLM 供应商的统一流式对话接口（R7）。
// 实现需在单独的 goroutine 中向返回的 channel 发送事件，并在结束/出错时关闭 channel。
type Provider interface {
	// Stream 发起一次流式对话。system 为 system prompt，tools 为可用工具定义。
	Stream(ctx context.Context, system string, tools []tool.Definition, msgs []message.Message) <-chan StreamEvent
}

// New 按 profile.Protocol 构造对应的 Provider（R7 工厂）。
// anthropic / openai 的具体实现分别在 T5 / T6 完成。
func New(p config.Profile) (Provider, error) {
	switch p.Protocol {
	case config.ProtocolAnthropic:
		return newAnthropic(p)
	case config.ProtocolOpenAI:
		return newOpenAI(p)
	default:
		return nil, fmt.Errorf("unknown protocol: %s", p.Protocol)
	}
}
