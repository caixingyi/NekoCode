package tui

import (
	"strings"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/session"
)

func TestPlanToggleAppendsModeSupplement(t *testing.T) {
	m := newTestModel(t)

	m.handleCommand("/plan")
	if len(m.sess.Messages) != 1 {
		t.Fatalf("messages = %d, want 1 enter 补充消息", len(m.sess.Messages))
	}
	enter := m.sess.Messages[0]
	if !enter.IsSupplement || enter.SupplementKind != message.SupplementMode ||
		enter.SupplementMode != message.SupplementModePlan || enter.SupplementCadence != message.SupplementCadenceFull || enter.SupplementSeq != 2 {
		t.Errorf("enter 补充消息 = %+v", enter)
	}

	m.handleCommand("/plan")
	if len(m.sess.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (enter + exit)", len(m.sess.Messages))
	}
	exit := m.sess.Messages[1]
	if !exit.IsSupplement || exit.SupplementKind != message.SupplementMode ||
		exit.SupplementMode != message.SupplementModeNormal || exit.SupplementCadence != message.SupplementCadenceFull || exit.SupplementSeq != 3 {
		t.Errorf("exit 补充消息 = %+v", exit)
	}
}

func TestSupplementsHiddenInView(t *testing.T) {
	note := message.NewModeSupplement(2, true, true, "SECRET-NOTE")
	if got := renderMessage(note, 80); got != "" {
		t.Errorf("系统补充消息 should not render, got %q", got)
	}
	if got := renderMessage(message.NewMessage(message.RoleUser, "hi"), 80); !strings.Contains(got, "hi") {
		t.Errorf("real user message lost: %q", got)
	}
}

func TestCacheTextFormatting(t *testing.T) {
	if got := cacheText(provider.Usage{}); got != "缓存 —" {
		t.Errorf("unknown cache = %q", got)
	}
	known := provider.Usage{InputTokens: 100, CacheReadTokens: 60, CacheUsageKnown: true}
	if got := cacheText(known); got != "缓存 60/100 60%" {
		t.Errorf("cache text = %q", got)
	}
	if got := compactCacheText(known); got != "缓存 60%" {
		t.Errorf("compact cache text = %q", got)
	}
	first := provider.Usage{InputTokens: 12000, CacheCreationTokens: 12000, CacheUsageKnown: true}
	if got := cacheText(first); got != "缓存 0% (写 12.0k)" {
		t.Errorf("first-turn cache text = %q", got)
	}
	if formatTokens(999) != "999" || formatTokens(1500) != "1.5k" || formatTokens(1_500_000) != "1.5m" {
		t.Errorf("formatTokens = %q/%q/%q", formatTokens(999), formatTokens(1500), formatTokens(1_500_000))
	}
}

func TestHeaderCacheAndWidth(t *testing.T) {
	m := newTestModel(t) // width 80
	m.turnUsage = provider.Usage{InputTokens: 100, CacheReadTokens: 60, CacheUsageKnown: true}

	if h := m.header(); strings.Contains(h, "/help") || !strings.Contains(h, "缓存 60%") {
		t.Errorf("width 80 header = %q", h)
	}
	m.width = 60
	if h := m.header(); strings.Contains(h, "/help") || !strings.Contains(h, "缓存 60%") {
		t.Errorf("width 60 header = %q", h)
	}
	m.width = 120
	if h := m.header(); !strings.Contains(h, "缓存 60/100 60%") || !strings.Contains(h, "/help") {
		t.Errorf("width 120 header = %q", h)
	}
}

func TestSwitchRebuildsInstructions(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"a": {Protocol: config.ProtocolAnthropic, Model: "m", APIKey: "k", SystemPrompt: "CUSTOM-A"},
		"b": {Protocol: config.ProtocolAnthropic, Model: "m", APIKey: "k", SystemPrompt: "CUSTOM-B"},
	}}
	m, err := New(cfg, "a", session.NewStore(t.TempDir()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.instructions.System(), "CUSTOM-A") {
		t.Error("initial instructions missing CUSTOM-A")
	}
	m.handleCommand("/switch b")
	if !strings.Contains(m.instructions.System(), "CUSTOM-B") {
		t.Error("instructions not rebuilt on switch")
	}
}

func TestResumeSetsEnvRefreshAndRebuildsInstructions(t *testing.T) {
	m := newTestModel(t)
	p := m.cfg.Profiles["claude"]
	p.SystemPrompt = "RESUMED"
	m.cfg.Profiles["claude"] = p

	s := session.NewSession("claude")
	s.Messages = []message.Message{message.NewMessage(message.RoleUser, "旧话题")}
	if err := m.store.Save(s); err != nil {
		t.Fatal(err)
	}

	m.envNeedsRefresh = false
	m.handleCommand("/resume 1")
	if m.sess.ID != s.ID {
		t.Fatalf("resumed session = %q, want %q", m.sess.ID, s.ID)
	}
	if !m.envNeedsRefresh {
		t.Error("resume should request env refresh")
	}
	if !strings.Contains(m.instructions.System(), "RESUMED") {
		t.Error("instructions not rebuilt on resume")
	}
}

func TestClearAndNewSetEnvRefresh(t *testing.T) {
	m := newTestModel(t)
	m.envNeedsRefresh = false
	m.handleCommand("/clear")
	if !m.envNeedsRefresh {
		t.Error("/clear should request env refresh")
	}
	m.envNeedsRefresh = false
	m.handleCommand("/new")
	if !m.envNeedsRefresh {
		t.Error("/new should request env refresh")
	}
}

func TestStartRunResetsTurnUsageAndInjectsEnv(t *testing.T) {
	srv := sseServer(t, anthropicText("ok")+anthropicStop())
	defer srv.Close()

	m := anthropicModel(t, srv.URL, t.TempDir())
	m.turnUsage = provider.Usage{InputTokens: 99, CacheUsageKnown: true}
	h := newHarness(t, m)
	m = h.run(m.startRun("hi"))

	if m.turnUsage.InputTokens != 0 {
		t.Errorf("turnUsage not reset from previous run: %+v", m.turnUsage)
	}
	if len(m.sess.Messages) == 0 || !m.sess.Messages[0].IsSupplement || m.sess.Messages[0].SupplementKind != message.SupplementEnv {
		t.Errorf("first message is not env 补充消息: %+v", m.sess.Messages)
	}
	if m.envNeedsRefresh {
		t.Error("envNeedsRefresh should be cleared after startRun")
	}
}
