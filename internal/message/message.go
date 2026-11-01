// Package message 定义 NekoCode 内部统一的对话消息模型，
// 并提供按目标 provider 归一化（Anthropic content blocks / OpenAI messages）的能力。
package message

import (
	"fmt"
	"time"

	"nekocode/internal/config"
	"nekocode/internal/tool"
)

// Role 是消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ToolCall 是模型发起的一次工具调用（assistant 消息携带）。
type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResult 是一次工具执行结果（回灌的 user 消息携带）。
type ToolResult struct {
	ID      string `json:"id"` // 对应 ToolCall.ID
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// Message 是 NekoCode 内部统一的对话消息模型。
type Message struct {
	Role      Role   `json:"role"`
	Content   string `json:"content"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// 系统补充消息：以 user 角色承载，正文带特殊标签；内部逻辑用这些字段判断。
	IsSupplement      bool         `json:"is_supplement,omitempty"`
	SupplementKind    string       `json:"supplement_kind,omitempty"`    // env | mode | summary
	SupplementSeq     int          `json:"supplement_seq,omitempty"`     // env=1；mode/summary>=2
	SupplementMode    string       `json:"supplement_mode,omitempty"`    // plan | normal
	SupplementCadence string       `json:"supplement_cadence,omitempty"` // full | brief
	ToolCalls         []ToolCall   `json:"tool_calls,omitempty"`   // assistant 发起
	ToolResults       []ToolResult `json:"tool_results,omitempty"` // user 回灌
	CreatedAt         time.Time    `json:"created_at"`
}

// NewMessage 构造一条带时间戳的消息。
func NewMessage(role Role, content string) Message {
	return Message{Role: role, Content: content, CreatedAt: time.Now()}
}

// Normalize 按 profile.Protocol 把内部消息与工具定义归一化为对应 provider 的请求体。
func Normalize(profile config.Profile, system string, tools []tool.Definition, msgs []Message) (any, error) {
	switch profile.Protocol {
	case config.ProtocolAnthropic:
		return normalizeAnthropic(profile, system, tools, msgs), nil
	case config.ProtocolOpenAI:
		return normalizeOpenAI(profile, system, tools, msgs), nil
	default:
		return nil, fmt.Errorf("unknown protocol: %s", profile.Protocol)
	}
}
