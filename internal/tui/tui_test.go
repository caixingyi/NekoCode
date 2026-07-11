package tui

import (
	"strings"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/session"
)

func TestRenderMessage(t *testing.T) {
	if s := renderMessage(message.NewMessage(message.RoleUser, "你好")); !strings.Contains(s, "你好") {
		t.Errorf("user render = %q", s)
	}
	a := renderMessage(message.Message{Role: message.RoleAssistant, Content: "回复", Thinking: "思考"})
	if !strings.Contains(a, "回复") || !strings.Contains(a, "思考") {
		t.Errorf("assistant render = %q", a)
	}
	s := renderMessage(message.Message{Role: message.RoleSystem, IsSummary: true, Content: "旧对话摘要"})
	if !strings.Contains(s, "[摘要]") || !strings.Contains(s, "旧对话摘要") {
		t.Errorf("summary render = %q", s)
	}
}

func TestWelcomeRender(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"p": {Protocol: config.ProtocolOpenAI, Model: "m", APIKey: "k"},
	}}
	m, err := New(cfg, "p", session.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	m.width = 60
	w := m.welcome()
	if !strings.Contains(w, "NekoCode") || !strings.Contains(w, "o.o") || !strings.Contains(w, "开始") {
		t.Errorf("welcome missing elements: %q", w)
	}
}

func TestViewDoesNotPanic(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"p": {Protocol: config.ProtocolOpenAI, Model: "m", APIKey: "k"},
	}}
	m, err := New(cfg, "p", session.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	m.sess.Messages = []message.Message{
		message.NewMessage(message.RoleUser, "你好"),
		message.NewMessage(message.RoleAssistant, "你好呀"),
	}
	m.streaming = true
	m.streamBuf = "生成中"
	m.thinkBuf = "思考中"
	_ = m.View() // 不 panic 即可
}
