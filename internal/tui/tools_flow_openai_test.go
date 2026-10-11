package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/session"
	"nekocode/internal/tool"
)

func openaiToolTestModel(t *testing.T, srvURL, root string) *Model {
	t.Helper()
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"openai": {Protocol: config.ProtocolOpenAI, Model: "m", APIKey: "k", BaseURL: srvURL},
	}}
	reg := tool.NewRegistry(root)
	tool.RegisterAll(reg, root)
	m, err := New(cfg, "openai", session.NewStore(t.TempDir()), reg)
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	return m
}

func TestToolFlowOpenAIEndToEnd(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("openai-read"), 0o644)
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if atomic.AddInt32(&n, 1) == 1 {
			w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read_file","arguments":"{\"path\":\"a.txt\"}"}}]}}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
				`data: [DONE]` + "\n\n"))
			return
		}
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"读到了"}}]}` + "\n\n" + `data: [DONE]` + "\n\n"))
	}))
	defer srv.Close()

	m := openaiToolTestModel(t, srv.URL, root)
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "读 a.txt")}
	m = drive(t, m, m.startStream(), 'y')

	msgs := m.sess.Messages
	if len(msgs) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(msgs), msgs)
	}
	if len(msgs[2].ToolResults) != 1 || msgs[2].ToolResults[0].Content != "openai-read" {
		t.Errorf("tool result = %+v", msgs[2].ToolResults)
	}
	if msgs[3].Content != "读到了" {
		t.Errorf("final = %q", msgs[3].Content)
	}
}

func TestToolFlowMultipleTools(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("AA"), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("BB"), 0o644)
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if atomic.AddInt32(&n, 1) == 1 {
			// 一次回复请求两个工具（R17）。
			w.Write([]byte(
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"read_file\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"a.txt\\\"}\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t2\",\"name\":\"read_file\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"b.txt\\\"}\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			return
		}
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"两个都读了\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	m := toolTestModel(t, srv.URL, root)
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "读 a 和 b")}
	m = drive(t, m, m.startStream(), 'y')

	// assistant 应含 2 个 tool_calls，回灌的 user 消息应含 2 个 tool_results
	var callCount, resCount int
	for _, msg := range m.sess.Messages {
		callCount += len(msg.ToolCalls)
		resCount += len(msg.ToolResults)
	}
	if callCount != 2 || resCount != 2 {
		t.Fatalf("callCount=%d resCount=%d, want 2/2: %+v", callCount, resCount, m.sess.Messages)
	}
}
