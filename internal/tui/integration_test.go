package tui

import (
	"strings"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/session"
)

// TestIntegrationAnthropicStreamReconstruct：一家端到端——thinking/text 分片被正确
// 累积进历史，签名不丢。
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
	srv := sseServer(t, sse)
	defer srv.Close()

	m := testModel(t, config.Profile{
		Protocol: config.ProtocolAnthropic, Model: "m", APIKey: "k", BaseURL: srv.URL,
	}, "claude", t.TempDir())
	h := newHarness(t, m)
	m = h.run(h.m.startRun("hi"))

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
	if m.running {
		t.Error("should not be running after done")
	}
}

func TestIntegrationOpenAIStreamReconstruct(t *testing.T) {
	srv := sseServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n\ndata: [DONE]\n\n")
	defer srv.Close()

	m := testModel(t, config.Profile{
		Protocol: config.ProtocolOpenAI, Model: "m", APIKey: "k", BaseURL: srv.URL,
	}, "openai", t.TempDir())
	h := newHarness(t, m)
	m = h.run(h.m.startRun("hi"))

	if len(m.sess.Messages) != 2 || m.sess.Messages[1].Content != "Hi there" {
		t.Errorf("messages = %+v", m.sess.Messages)
	}
}

// TestIntegrationStreamErrorKeepsPartial：流中途报错时已生成文本仍入库，并提示错误。
func TestIntegrationStreamErrorKeepsPartial(t *testing.T) {
	sse := strings.Join([]string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"部分回复\"}}\n\n",
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n",
	}, "")
	srv := sseServer(t, sse)
	defer srv.Close()

	m := testModel(t, config.Profile{
		Protocol: config.ProtocolAnthropic, Model: "m", APIKey: "k", BaseURL: srv.URL,
	}, "claude", t.TempDir())
	h := newHarness(t, m)
	m = h.run(h.m.startRun("hi"))

	if len(m.sess.Messages) != 2 || m.sess.Messages[1].Content != "部分回复" {
		t.Errorf("partial text not kept on error: %+v", m.sess.Messages)
	}
	if m.errMsg == "" {
		t.Error("error message not set")
	}
	pairedHistory(t, m)
}

// TestAutoSummarizeTrigger：超过阈值时先压缩再发送，输入内容不丢。
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
		t.Error("expected pendingSend=true for over-threshold submit")
	}
	if cmd == nil {
		t.Error("expected compact cmd returned")
	}
	// 用户消息不再由 TUI 追加（改由 agent 的快照带回），但内容必须被暂存下来。
	if m.pendingText != "新问题" {
		t.Errorf("pendingText = %q, want 新问题", m.pendingText)
	}
	if len(m.sess.Messages) != 20 {
		t.Errorf("TUI 不该自行追加用户消息，len = %d", len(m.sess.Messages))
	}

	// 压缩完成后应自动继续发送。
	mm, cmd = m.handleCompactDone(compactDoneMsg{msgs: []message.Message{message.NewMessage(message.RoleUser, "摘要")}, n: 14})
	m = mm.(*Model)
	if m.pendingSend {
		t.Error("pendingSend 应已清空")
	}
	if cmd == nil {
		t.Error("压缩完成后应继续发起循环")
	}
}
