package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nekocode/internal/tool"
)

func collectToolCalls(evs []StreamEvent) []StreamEvent {
	var out []StreamEvent
	for _, ev := range evs {
		if ev.Type == EventToolCall {
			out = append(out, ev)
		}
	}
	return out
}

func TestAnthropicStreamToolUse(t *testing.T) {
	// tool_use block：参数分两段 input_json_delta 拼接。
	sse := strings.Join([]string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu_1\",\"name\":\"read_file\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"a.txt\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newAnthropic(anthropicProfile(srv.URL))
	tools := []tool.Definition{{Name: "read_file", Schema: map[string]any{"type": "object"}}}
	evs := drain(p.Stream(context.Background(), "sys", tools, nil))

	tcs := collectToolCalls(evs)
	if len(tcs) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(tcs))
	}
	tc := tcs[0].ToolCall
	if tc.ID != "tu_1" || tc.Name != "read_file" || tc.Input["path"] != "a.txt" {
		t.Errorf("tool call = %+v", tc)
	}
}

func TestOpenAIStreamToolCalls(t *testing.T) {
	// tool_calls：id/name 首片给出，arguments 分片拼接，finish_reason=tool_calls 触发 flush。
	sse := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":""}}]}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, "",
		`data: [DONE]`, "",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newOpenAI(openaiProfile(srv.URL))
	tools := []tool.Definition{{Name: "read_file", Schema: map[string]any{"type": "object"}}}
	evs := drain(p.Stream(context.Background(), "sys", tools, nil))

	tcs := collectToolCalls(evs)
	if len(tcs) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(tcs))
	}
	tc := tcs[0].ToolCall
	if tc.ID != "call_1" || tc.Name != "read_file" || tc.Input["path"] != "a.txt" {
		t.Errorf("tool call = %+v", tc)
	}
}

func TestOpenAIStreamMultipleToolCalls(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"c2","function":{"name":"read_file","arguments":"{\"path\":\"b\"}"}}]}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, "",
		`data: [DONE]`, "",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newOpenAI(openaiProfile(srv.URL))
	evs := drain(p.Stream(context.Background(), "sys", nil, nil))
	tcs := collectToolCalls(evs)
	if len(tcs) != 2 || tcs[0].ToolCall.Input["path"] != "a" || tcs[1].ToolCall.Input["path"] != "b" {
		t.Fatalf("want 2 tool calls a/b, got %+v", tcs)
	}
}
