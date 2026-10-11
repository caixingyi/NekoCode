package message

import (
	"encoding/json"
	"strings"

	"nekocode/internal/config"
	"nekocode/internal/tool"
)

// ---- Anthropic 请求体 ----

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     map[string]any  `json:"input,omitempty"`       // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   json.RawMessage `json:"content,omitempty"`     // tool_result（字符串或 blocks）
	IsError   bool            `json:"is_error,omitempty"`    // tool_result
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Stream      bool               `json:"stream"`
	Temperature *float64           `json:"temperature,omitempty"`
	Thinking    *anthropicThinking `json:"thinking,omitempty"`
}

// normalizeAnthropic 按 profile.Protocol 把内部消息与工具定义归一化为对应 provider 的请求体。
func normalizeAnthropic(profile config.Profile, system string, tools []tool.Definition, msgs []Message) *anthropicRequest {
	var systemParts []string
	if strings.TrimSpace(system) != "" {
		systemParts = append(systemParts, system)
	}

	var conv []anthropicMessage
	//	对每条非 system 消息，按 role 转成 anthropicMessage
	for _, m := range msgs {
		if m.Role == RoleSystem {
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
	for _, d := range tools {
		req.Tools = append(req.Tools, anthropicTool{Name: d.Name, Description: d.Description, InputSchema: d.Schema})
	}

	if profile.Thinking != nil && profile.Thinking.Enabled {
		req.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: profile.Thinking.BudgetTokens}
		t := 1.0
		req.Temperature = &t
	} else if profile.Temperature != nil {
		req.Temperature = profile.Temperature
	}
	return req
}

// toAnthropicMessage：user 的 tool_results 转成 tool_result blocks；
// assistant 的 thinking/text + tool_calls 转成对应 blocks。
func toAnthropicMessage(m Message) anthropicMessage {
	var blocks []anthropicContentBlock

	// tool_results 优先级最高，直接转成 tool_result blocks。
	if len(m.ToolResults) > 0 {
		for _, tr := range m.ToolResults {
			blocks = append(blocks, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: tr.ID,
				Content:   jsonString(tr.Content),
				IsError:   tr.IsError,
			})
		}
		return anthropicMessage{Role: "user", Content: blocks}
	}

	// thinking的优先级高于 text，且 thinking/signature 都非空时才输出 thinking block。
	if m.Thinking != "" && m.Signature != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "thinking", Thinking: m.Thinking, Signature: m.Signature})
	}
	// text block 仅在 content 非空时输出。
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
	}
	// tool_call block 仅在有 tool calls 时输出。
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: nonNilInput(tc.Input)})
	}
	// 如果没有任何 block，则输出一个空 text block。
	if len(blocks) == 0 {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: ""})
	}
	return anthropicMessage{Role: string(m.Role), Content: blocks}
}

// ---- OpenAI 请求体 ----

type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Tools       []openaiTool    `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
}

func normalizeOpenAI(profile config.Profile, system string, tools []tool.Definition, msgs []Message) *openaiRequest {
	var out []openaiMessage
	if strings.TrimSpace(system) != "" {
		out = append(out, openaiMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		if len(m.ToolResults) > 0 {
			// 每个工具结果一条 role=tool 消息。
			for _, tr := range m.ToolResults {
				out = append(out, openaiMessage{Role: "tool", ToolCallID: tr.ID, Content: tr.Content})
			}
			continue
		}
		om := openaiMessage{Role: string(m.Role), Content: m.Content}
		for _, tc := range m.ToolCalls {
			var oc openaiToolCall
			oc.ID = tc.ID
			oc.Type = "function"
			oc.Function.Name = tc.Name
			oc.Function.Arguments = marshalArgs(tc.Input)
			om.ToolCalls = append(om.ToolCalls, oc)
		}
		out = append(out, om)
	}

	req := &openaiRequest{
		Model:       profile.Model,
		Messages:    out,
		Stream:      true,
		MaxTokens:   profile.MaxTokens,
		Temperature: profile.Temperature,
	}
	for _, d := range tools {
		var ot openaiTool
		ot.Type = "function"
		ot.Function.Name = d.Name
		ot.Function.Description = d.Description
		ot.Function.Parameters = d.Schema
		req.Tools = append(req.Tools, ot)
	}
	return req
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func marshalArgs(input map[string]any) string {
	if input == nil {
		return "{}"
	}
	b, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func nonNilInput(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	return in
}
