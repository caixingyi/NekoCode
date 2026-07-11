package tui

import (
	"strings"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/session"
)

func newTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"claude": {Protocol: config.ProtocolAnthropic, Model: "claude-x", APIKey: "k"},
		"openai": {Protocol: config.ProtocolOpenAI, Model: "gpt-x", APIKey: "k"},
	}}
	m, err := New(cfg, "claude", session.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	return m
}

func TestCommandClear(t *testing.T) {
	m := newTestModel(t)
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "hi")}
	m.handleCommand("/clear")
	if len(m.sess.Messages) != 0 {
		t.Errorf("messages not cleared")
	}
}

func TestCommandSwitch(t *testing.T) {
	m := newTestModel(t)
	m.handleCommand("/switch openai")
	if m.profileName != "openai" || m.profile.Protocol != config.ProtocolOpenAI {
		t.Errorf("switch failed: %s", m.profileName)
	}
	if len(m.sess.ProfileHistory) != 1 {
		t.Errorf("switch not recorded")
	}
}

func TestCommandSwitchInvalid(t *testing.T) {
	m := newTestModel(t)
	m.handleCommand("/switch nope")
	if m.errMsg == "" {
		t.Errorf("expected error for invalid profile")
	}
	if m.profileName != "claude" {
		t.Errorf("profile changed on failed switch")
	}
}

func TestCommandThinkToggle(t *testing.T) {
	m := newTestModel(t) // claude
	m.handleCommand("/think")
	if m.profile.Thinking == nil || !m.profile.Thinking.Enabled {
		t.Errorf("think should enable on first toggle")
	}
	m.handleCommand("/think")
	if m.profile.Thinking.Enabled {
		t.Errorf("think should disable on second toggle")
	}
}

func TestCommandThinkOpenAINotSupported(t *testing.T) {
	m := newTestModel(t)
	m.handleCommand("/switch openai")
	m.handleCommand("/think")
	if m.notice == "" || m.profile.Thinking != nil {
		t.Errorf("openai think should be rejected, notice=%q", m.notice)
	}
}

func TestCommandResumeListAndLoad(t *testing.T) {
	m := newTestModel(t)
	s := session.NewSession("openai")
	s.Messages = []message.Message{message.NewMessage(message.RoleUser, "旧话题")}
	if err := m.store.Save(s); err != nil {
		t.Fatal(err)
	}

	// 列表
	m.handleCommand("/resume")
	if !strings.Contains(m.notice, "旧话题") {
		t.Errorf("resume list missing session: %q", m.notice)
	}

	// 加载 1 号
	m.handleCommand("/resume 1")
	if len(m.sess.Messages) != 1 || m.sess.Messages[0].Content != "旧话题" {
		t.Errorf("resume load failed: %+v", m.sess.Messages)
	}
	if m.profileName != "openai" {
		t.Errorf("resume should restore profile to openai, got %s", m.profileName)
	}
}

func TestCommandUnknown(t *testing.T) {
	m := newTestModel(t)
	m.handleCommand("/foobar")
	if m.errMsg == "" {
		t.Errorf("expected error for unknown command")
	}
}
