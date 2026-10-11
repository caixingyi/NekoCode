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

// StreamEvent 是 Provider 流式输出的统一事件。
//
// 对于 EventThinking：Text 为思考文本增量；thinking block 结束时发一条 Text 空、
// Signature 非空的事件。EventToolCall 时 ToolCall 携带完整调用（含拼接好的参数）。
type StreamEvent struct {
	Type      EventType
	Text      string
	Err       error
	Signature string
	ToolCall  message.ToolCall
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
