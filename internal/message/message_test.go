package message

import (
	"encoding/json"
	"strings"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/tool"
)

func prof(protocol string) config.Profile {
	return config.Profile{Protocol: protocol, Model: "m", APIKey: "k", BaseURL: "https://x"}
}

func TestNormalizeAnthropicBasic(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	msgs := []Message{
		NewMessage(RoleUser, "你好"),
		NewMessage(RoleAssistant, "你好呀"),
	}
	reqAny, err := Normalize(p, "你是助手", nil, msgs)
	if err != nil {
		t.Fatal(err)
	}
	req := reqAny.(*anthropicRequest)
	if len(req.System) != 1 || req.System[0].Text != "你是助手" {
		t.Errorf("System = %+v", req.System)
	}
	if req.System[0].CacheControl == nil || req.System[0].CacheControl.Type != "ephemeral" {
		t.Errorf("system cache_control = %+v", req.System[0].CacheControl)
	}
	if req.MaxTokens != config.DefaultMaxTokens {
		t.Errorf("MaxTokens = %d, want default %d", req.MaxTokens, config.DefaultMaxTokens)
	}
	if !req.Stream {
		t.Errorf("Stream = false, want true")
	}
	if len(req.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(req.Messages))
	}
	// user 消息应为单个 text block。
	if len(req.Messages[0].Content) != 1 || req.Messages[0].Content[0].Type != "text" {
		t.Errorf("user content blocks = %+v", req.Messages[0].Content)
	}
	if req.Messages[0].Content[0].Text != "你好" {
		t.Errorf("user text = %q", req.Messages[0].Content[0].Text)
	}
}

func TestNormalizeAnthropicThinkingConfig(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	p.Thinking = &config.ThinkingConfig{Enabled: true, BudgetTokens: 2048}
	temp := 0.5
	p.Temperature = &temp
	reqAny, _ := Normalize(p, "sys", nil, []Message{NewMessage(RoleUser, "q")})
	req := reqAny.(*anthropicRequest)
	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens != 2048 {
		t.Errorf("Thinking = %+v", req.Thinking)
	}
	if req.Temperature == nil || *req.Temperature != 1.0 {
		t.Errorf("Temperature = %v, want 1.0 (forced when thinking enabled)", req.Temperature)
	}
}

func TestNormalizeAnthropicThinkingHistory(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	msgs := []Message{
		{Role: RoleAssistant, Content: "ans", Thinking: "thought", Signature: "sig"},
	}
	reqAny, _ := Normalize(p, "sys", nil, msgs)
	req := reqAny.(*anthropicRequest)
	blocks := req.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2 (thinking + text)", len(blocks))
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "thought" || blocks[0].Signature != "sig" {
		t.Errorf("thinking block = %+v", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].Text != "ans" {
		t.Errorf("text block = %+v", blocks[1])
	}
}

func TestNormalizeAnthropicThinkingWithoutSignatureDropped(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	msgs := []Message{{Role: RoleAssistant, Content: "ans", Thinking: "thought"}} // 无 signature
	reqAny, _ := Normalize(p, "sys", nil, msgs)
	req := reqAny.(*anthropicRequest)
	blocks := req.Messages[0].Content
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Errorf("blocks = %+v, want only text (thinking without signature dropped)", blocks)
	}
}

func TestNormalizeOpenAIBasic(t *testing.T) {
	p := prof(config.ProtocolOpenAI)
	msgs := []Message{
		NewMessage(RoleUser, "你好"),
		{Role: RoleAssistant, Content: "ans", Thinking: "t", Signature: "s"},
	}
	reqAny, err := Normalize(p, "你是助手", nil, msgs)
	if err != nil {
		t.Fatal(err)
	}
	req := reqAny.(*openaiRequest)
	// 首条为 system，随后 user、assistant；assistant 的 thinking/signature 被丢弃。
	if len(req.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "你是助手" {
		t.Errorf("msg[0] = %+v", req.Messages[0])
	}
	if req.Messages[2].Role != "assistant" || req.Messages[2].Content != "ans" {
		t.Errorf("msg[2] = %+v (thinking/signature should be dropped)", req.Messages[2])
	}
}

func TestNormalizeOpenAIIgnoresThinking(t *testing.T) {
	p := prof(config.ProtocolOpenAI)
	p.Thinking = &config.ThinkingConfig{Enabled: true, BudgetTokens: 2048}
	reqAny, _ := Normalize(p, "sys", nil, []Message{NewMessage(RoleUser, "q")})
	req := reqAny.(*openaiRequest)
	// openaiRequest 没有 thinking 字段，此处仅验证不报错且请求正常。
	if req.Model != "m" {
		t.Errorf("Model = %q", req.Model)
	}
}

func TestNormalizeUnknownProtocol(t *testing.T) {
	p := prof("gemini")
	if _, err := Normalize(p, "sys", nil, nil); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestNormalizeAnthropicTemperaturePassthrough(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	temp := 0.7
	p.Temperature = &temp
	reqAny, _ := Normalize(p, "sys", nil, []Message{NewMessage(RoleUser, "q")})
	req := reqAny.(*anthropicRequest)
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", req.Temperature)
	}
}

func TestNormalizeAnthropicCacheBreakpoints(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	defs := []tool.Definition{
		{Name: "a", Description: "A", Schema: map[string]any{"type": "object"}},
		{Name: "b", Description: "B", Schema: map[string]any{"type": "object"}},
	}
	reqAny, err := Normalize(p, "sys", defs, []Message{NewMessage(RoleUser, "q")})
	if err != nil {
		t.Fatal(err)
	}
	req := reqAny.(*anthropicRequest)
	if len(req.System) != 1 || req.System[0].CacheControl == nil || req.System[0].CacheControl.Type != "ephemeral" {
		t.Errorf("system cache_control = %+v", req.System)
	}
	if len(req.Tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(req.Tools))
	}
	if req.Tools[0].CacheControl != nil {
		t.Errorf("first tool should not have cache_control: %+v", req.Tools[0])
	}
	if req.Tools[1].CacheControl == nil || req.Tools[1].CacheControl.Type != "ephemeral" {
		t.Errorf("last tool cache_control = %+v", req.Tools[1].CacheControl)
	}
	raw, _ := json.Marshal(req)
	if n := strings.Count(string(raw), `"cache_control":{"type":"ephemeral"}`); n != 2 {
		t.Errorf("cache_control occurrences = %d, want 2: %s", n, raw)
	}
}

func TestNormalizeOpenAINoCacheControl(t *testing.T) {
	p := prof(config.ProtocolOpenAI)
	defs := []tool.Definition{{Name: "a", Description: "A", Schema: map[string]any{"type": "object"}}}
	reqAny, err := Normalize(p, "sys", defs, []Message{NewMessage(RoleUser, "q")})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(reqAny)
	if strings.Contains(string(raw), "cache_control") {
		t.Errorf("OpenAI request must not contain cache_control: %s", raw)
	}
}
