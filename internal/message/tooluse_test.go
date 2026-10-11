package message

import (
	"encoding/json"
	"strings"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/tool"
)

var toolDefs = []tool.Definition{
	{Name: "read_file", Description: "read", Schema: map[string]any{"type": "object"}},
}

func TestNormalizeAnthropicToolsAndBlocks(t *testing.T) {
	p := prof(config.ProtocolAnthropic)
	msgs := []Message{
		NewMessage(RoleUser, "读 a.txt"),
		{Role: RoleAssistant, Content: "好的", ToolCalls: []ToolCall{{ID: "t1", Name: "read_file", Input: map[string]any{"path": "a.txt"}}}},
		{Role: RoleUser, ToolResults: []ToolResult{{ID: "t1", Content: "file content"}}},
	}
	reqAny, err := Normalize(p, "sys", toolDefs, msgs)
	if err != nil {
		t.Fatal(err)
	}
	req := reqAny.(*anthropicRequest)
	if len(req.Tools) != 1 || req.Tools[0].Name != "read_file" {
		t.Errorf("tools = %+v", req.Tools)
	}
	// assistant 消息应含 text + tool_use
	asm := req.Messages[1]
	if asm.Role != "assistant" {
		t.Fatalf("msg[1] role = %s", asm.Role)
	}
	var hasToolUse bool
	for _, b := range asm.Content {
		if b.Type == "tool_use" && b.ID == "t1" && b.Name == "read_file" {
			hasToolUse = true
		}
	}
	if !hasToolUse {
		t.Errorf("assistant missing tool_use block: %+v", asm.Content)
	}
	// tool_result 消息应为 user + tool_result block
	trm := req.Messages[2]
	if trm.Role != "user" || len(trm.Content) != 1 || trm.Content[0].Type != "tool_result" || trm.Content[0].ToolUseID != "t1" {
		t.Errorf("tool_result msg = %+v", trm)
	}
	// content 应为 JSON 字符串 "file content"
	var s string
	if err := json.Unmarshal(trm.Content[0].Content, &s); err != nil || s != "file content" {
		t.Errorf("tool_result content = %s (%v)", trm.Content[0].Content, err)
	}
}

func TestNormalizeOpenAIToolsAndBlocks(t *testing.T) {
	p := prof(config.ProtocolOpenAI)
	msgs := []Message{
		NewMessage(RoleUser, "读 a.txt"),
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "t1", Name: "read_file", Input: map[string]any{"path": "a.txt"}}}},
		{Role: RoleUser, ToolResults: []ToolResult{{ID: "t1", Content: "file content"}}},
	}
	reqAny, err := Normalize(p, "sys", toolDefs, msgs)
	if err != nil {
		t.Fatal(err)
	}
	req := reqAny.(*openaiRequest)
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "read_file" || req.Tools[0].Type != "function" {
		t.Errorf("tools = %+v", req.Tools)
	}
	// 找到含 tool_calls 的 assistant 消息
	var asm *openaiMessage
	for i := range req.Messages {
		if req.Messages[i].Role == "assistant" && len(req.Messages[i].ToolCalls) > 0 {
			asm = &req.Messages[i]
		}
	}
	if asm == nil {
		t.Fatal("no assistant with tool_calls")
	}
	tc := asm.ToolCalls[0]
	if tc.ID != "t1" || tc.Function.Name != "read_file" || !strings.Contains(tc.Function.Arguments, "a.txt") {
		t.Errorf("tool_call = %+v", tc)
	}
	// tool 结果消息 role=tool + tool_call_id
	var toolMsg *openaiMessage
	for i := range req.Messages {
		if req.Messages[i].Role == "tool" {
			toolMsg = &req.Messages[i]
		}
	}
	if toolMsg == nil || toolMsg.ToolCallID != "t1" || toolMsg.Content != "file content" {
		t.Errorf("tool result msg = %+v", toolMsg)
	}
}

func TestNormalizeMultipleToolResults(t *testing.T) {
	p := prof(config.ProtocolOpenAI)
	msgs := []Message{
		{Role: RoleUser, ToolResults: []ToolResult{{ID: "t1", Content: "r1"}, {ID: "t2", Content: "r2"}}},
	}
	reqAny, _ := Normalize(p, "sys", toolDefs, msgs)
	req := reqAny.(*openaiRequest)
	count := 0
	for _, m := range req.Messages {
		if m.Role == "tool" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("want 2 tool messages, got %d", count)
	}
}
