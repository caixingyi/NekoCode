package message

import (
	"testing"

	"nekocode/internal/config"
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
	reqAny, err := Normalize(p, "你是助手", msgs)
	if err != nil {
		t.Fatal(err)
	}
	req := reqAny.(*anthropicRequest)
	if req.System != "你是助手" {
		t.Errorf("System = %q", req.System)
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
	reqAny, _ := Normalize(p, "sys", []Message{NewMessage(RoleUser, "q")})
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
	reqAny, _ := Normalize(p, "sys", msgs)
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
	reqAny, _ := Normalize(p, "sys", msgs)
	req := reqAny.(*anthropicRequest)
	blocks := req.Messages[0].Content
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Errorf("blocks = %+v, want only text (thinking without signature dropped)", blocks)
	}
}

func TestNormalizeAnthropicSummaryMergedIntoSystem(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	msgs := []Message{
		{Role: RoleSystem, Content: "之前的对话摘要", IsSummary: true},
		NewMessage(RoleUser, "继续"),
	}
	reqAny, _ := Normalize(p, "身份", msgs)
	req := reqAny.(*anthropicRequest)
	if req.System != "身份\n\n之前的对话摘要" {
		t.Errorf("System = %q", req.System)
	}
	if len(req.Messages) != 1 {
		t.Errorf("len(Messages) = %d, want 1 (summary merged into system)", len(req.Messages))
	}
}

func TestNormalizeOpenAIBasic(t *testing.T) {
	p := prof(config.ProtocolOpenAI)
	msgs := []Message{
		NewMessage(RoleUser, "你好"),
		{Role: RoleAssistant, Content: "ans", Thinking: "t", Signature: "s"},
	}
	reqAny, err := Normalize(p, "你是助手", msgs)
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
	reqAny, _ := Normalize(p, "sys", []Message{NewMessage(RoleUser, "q")})
	req := reqAny.(*openaiRequest)
	// openaiRequest 没有 thinking 字段，此处仅验证不报错且请求正常。
	if req.Model != "m" {
		t.Errorf("Model = %q", req.Model)
	}
}

func TestNormalizeOpenAISummaryKeptAsSystem(t *testing.T) {
	p := prof(config.ProtocolOpenAI)
	msgs := []Message{
		{Role: RoleSystem, Content: "摘要", IsSummary: true},
		NewMessage(RoleUser, "q"),
	}
	reqAny, _ := Normalize(p, "身份", msgs)
	req := reqAny.(*openaiRequest)
	if len(req.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "身份" {
		t.Errorf("msg[0] = %+v", req.Messages[0])
	}
	if req.Messages[1].Role != "system" || req.Messages[1].Content != "摘要" {
		t.Errorf("msg[1] = %+v (summary should be kept as system)", req.Messages[1])
	}
	if req.Messages[2].Role != "user" {
		t.Errorf("msg[2] = %+v", req.Messages[2])
	}
}

func TestNormalizeUnknownProtocol(t *testing.T) {
	p := prof("gemini")
	if _, err := Normalize(p, "sys", nil); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestNormalizeAnthropicTemperaturePassthrough(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	temp := 0.7
	p.Temperature = &temp
	reqAny, _ := Normalize(p, "sys", []Message{NewMessage(RoleUser, "q")})
	req := reqAny.(*anthropicRequest)
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", req.Temperature)
	}
}
