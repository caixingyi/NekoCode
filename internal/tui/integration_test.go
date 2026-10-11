package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/session"
)

// pumpStream 直接把 stream channel 的事件喂给 handleStreamEvent，
// 模拟 bubbletea 循环驱动 TUI 累积，直到流结束。
func pumpStream(t *testing.T, m *Model) *Model {
	t.Helper()
	for {
		ev, ok := <-m.streamCh
		if !ok {
			m.finalizeStream()
			break
		}
		mm, _ := m.handleStreamEvent(ev)
		m = mm.(*Model)
		if !m.streaming {
			break
		}
	}
	return m
}

func TestIntegrationAnthropicStreamReconstruct(t *testing.T) {
	sse := strings.Join([]string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hmm\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	cfg := &config.Config{Profiles: map[string]config.Profile{
		"claude": {Protocol: config.ProtocolAnthropic, Model: "m", APIKey: "k", BaseURL: srv.URL},
	}}
	m, err := New(cfg, "claude", session.NewStore(t.TempDir()), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "hi")}
	m.startStream()
	m = pumpStream(t, m)

	if len(m.sess.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(m.sess.Messages))
	}
	last := m.sess.Messages[1]
	if last.Role != message.RoleAssistant || last.Content != "Hello world" {
		t.Errorf("assistant = %+v", last)
	}
	if last.Thinking != "hmm" || last.Signature != "sig" {
		t.Errorf("thinking/signature lost: %+v", last)
	}
	if m.streaming {
		t.Errorf("should not be streaming after done")
	}
}

func TestIntegrationOpenAIStreamReconstruct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{Profiles: map[string]config.Profile{
		"openai": {Protocol: config.ProtocolOpenAI, Model: "m", APIKey: "k", BaseURL: srv.URL},
	}}
	m, _ := New(cfg, "openai", session.NewStore(t.TempDir()), nil)
	m.width = 80
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "hi")}
	m.startStream()
	m = pumpStream(t, m)

	if len(m.sess.Messages) != 2 || m.sess.Messages[1].Content != "Hi there" {
		t.Errorf("messages = %+v", m.sess.Messages)
	}
}

func TestIntegrationStreamErrorKeepsPartial(t *testing.T) {
	// Anthropic 流：先发一段文字，再发 error 事件：已生成文本应保留（R26）。
	sse := strings.Join([]string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"部分回复\"}}\n\n",
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n",
	}, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	cfg := &config.Config{Profiles: map[string]config.Profile{
		"claude": {Protocol: config.ProtocolAnthropic, Model: "m", APIKey: "k", BaseURL: srv.URL},
	}}
	m, _ := New(cfg, "claude", session.NewStore(t.TempDir()), nil)
	m.width = 80
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "hi")}
	m.startStream()
	m = pumpStream(t, m)

	if len(m.sess.Messages) != 2 || m.sess.Messages[1].Content != "部分回复" {
		t.Errorf("partial text not kept on error: %+v", m.sess.Messages)
	}
	if m.errMsg == "" {
		t.Errorf("error message not set")
	}
}

func TestAutoSummarizeTrigger(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"claude": {Protocol: config.ProtocolAnthropic, Model: "claude-x", APIKey: "k", ContextWindow: 100},
	}}
	m, _ := New(cfg, "claude", session.NewStore(t.TempDir()), nil)
	m.width = 80
	for i := 0; i < 20; i++ {
		m.sess.Messages = append(m.sess.Messages, message.Message{Role: message.RoleUser, Content: strings.Repeat("x", 20)})
	}
	m.input.SetValue("新问题")

	mm, cmd := m.handleSubmit()
	m = mm.(*Model)
	if !m.pendingSend {
		t.Errorf("expected pendingSend=true for over-threshold submit")
	}
	if cmd == nil {
		t.Errorf("expected compact cmd returned")
	}
	if len(m.sess.Messages) != 21 {
		t.Errorf("user message not appended: %d", len(m.sess.Messages))
	}
	// 释放未执行的 compact goroutine 引用（cmd 未调用，无 HTTP 发生）。
	_ = context.Background()
}
