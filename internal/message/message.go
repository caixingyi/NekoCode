// Package message 定义 NekoCode 内部统一的对话消息模型，
// 并提供按目标 provider 归一化（Anthropic content blocks / OpenAI messages）的能力。
package message

import (
	"fmt"
	"strings"
	"time"

	"nekocode/internal/config"
)

// Role 是消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message 是 NekoCode 内部统一的对话消息模型（R9）。
type Message struct {
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	Thinking  string    `json:"thinking,omitempty"`  // Claude 思考文本
	Signature string    `json:"signature,omitempty"` // thinking 签名，回传 Anthropic 用
	IsSummary bool      `json:"is_summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// NewMessage 构造一条带时间戳的消息。
func NewMessage(role Role, content string) Message {
	return Message{Role: role, Content: content, CreatedAt: time.Now()}
}

// ---- Anthropic 请求体 ----

type anthropicContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Stream      bool               `json:"stream"`
	Temperature *float64           `json:"temperature,omitempty"`
	Thinking    *anthropicThinking `json:"thinking,omitempty"`
}

// ---- OpenAI 请求体 ----

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
}

// Normalize 按 profile.Protocol 把内部消息归一化为对应 provider 的请求体（R9）。
// system 为本次使用的 system prompt（通常为 profile.EffectiveSystemPrompt()，
// 摘要场景可传入自定义提示）。
func Normalize(profile config.Profile, system string, msgs []Message) (any, error) {
	switch profile.Protocol {
	case config.ProtocolAnthropic:
		return normalizeAnthropic(profile, system, msgs), nil
	case config.ProtocolOpenAI:
		return normalizeOpenAI(profile, system, msgs), nil
	default:
		return nil, fmt.Errorf("unknown protocol: %s", profile.Protocol)
	}
}

// normalizeAnthropic：system 拼接（profile system + 摘要 system 消息）放顶层；
// assistant 含 thinking+signature 时输出 [thinking, text] 否则 [text]；
// max_tokens 缺省 4096；开 thinking 时 temperature 强制 1.0。
func normalizeAnthropic(profile config.Profile, system string, msgs []Message) *anthropicRequest {
	var systemParts []string
	if strings.TrimSpace(system) != "" {
		systemParts = append(systemParts, system)
	}

	var conv []anthropicMessage
	for _, m := range msgs {
		if m.Role == RoleSystem {
			// 摘要等 system 消息并入顶层 system。
			if strings.TrimSpace(m.Content) != "" {
				systemParts = append(systemParts, m.Content)
			}
			continue
		}
		conv = append(conv, toAnthropicMessage(m))
	}

	req := &anthropicRequest{
		Model:     profile.Model,
		MaxTokens: profile.EffectiveMaxTokens(),
		System:    strings.Join(systemParts, "\n\n"),
		Messages:  conv,
		Stream:    true,
	}

	if profile.Thinking != nil && profile.Thinking.Enabled {
		req.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: profile.Thinking.BudgetTokens}
		// 开启 thinking 时 temperature 必须为 1.0。
		t := 1.0
		req.Temperature = &t
	} else if profile.Temperature != nil {
		req.Temperature = profile.Temperature
	}

	return req
}

// toAnthropicMessage 转换单条消息为 content blocks。
// 仅当 thinking 与 signature 都非空时才回传 thinking block（Anthropic 要求签名）。
func toAnthropicMessage(m Message) anthropicMessage {
	var blocks []anthropicContentBlock
	if m.Thinking != "" && m.Signature != "" {
		blocks = append(blocks, anthropicContentBlock{
			Type:      "thinking",
			Thinking:  m.Thinking,
			Signature: m.Signature,
		})
	}
	blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
	return anthropicMessage{Role: string(m.Role), Content: blocks}
}

// normalizeOpenAI：system 作为首条 system 消息；摘要 system 消息保留为 system 消息；
// user/assistant -> {role, content}，丢弃 thinking/signature；thinking 配置忽略（R10）。
func normalizeOpenAI(profile config.Profile, system string, msgs []Message) *openaiRequest {
	var out []openaiMessage
	if strings.TrimSpace(system) != "" {
		out = append(out, openaiMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		// role=system 的摘要消息原样保留；其余角色 content 取文本。
		out = append(out, openaiMessage{Role: string(m.Role), Content: m.Content})
	}

	req := &openaiRequest{
		Model:       profile.Model,
		Messages:    out,
		Stream:      true,
		MaxTokens:   profile.MaxTokens, // 0 -> omitempty
		Temperature: profile.Temperature,
	}
	return req
}
