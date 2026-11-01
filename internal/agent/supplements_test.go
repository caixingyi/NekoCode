package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nekocode/internal/environment"
	"nekocode/internal/message"
	"nekocode/internal/prompt"
	"nekocode/internal/provider"
)

func fakeEnv(workDir string) func(context.Context, string) environment.Info {
	return func(context.Context, string) environment.Info {
		return environment.Info{
			WorkDir:  workDir,
			OS:       "darwin",
			Arch:     "arm64",
			Shell:    "zsh",
			Time:     time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
			GitKnown: true,
			IsRepo:   true,
			Branch:   "main",
		}
	}
}

func providerUsage(input, output, read, create int, known bool) *provider.Usage {
	return &provider.Usage{
		InputTokens:         input,
		OutputTokens:        output,
		CacheReadTokens:     read,
		CacheCreationTokens: create,
		CacheUsageKnown:     known,
	}
}

func countSupplementKind(msgs []message.Message, kind string) int {
	n := 0
	for _, m := range msgs {
		if m.IsSupplement && m.SupplementKind == kind {
			n++
		}
	}
	return n
}

func lastModeCadence(msgs []message.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].IsSupplement && msgs[i].SupplementKind == message.SupplementMode {
			return msgs[i].SupplementCadence
		}
	}
	return ""
}

func TestEnvSupplementPrependedAndRefreshed(t *testing.T) {
	old := message.NewEnvSupplement("旧环境：/old")
	history := []message.Message{old, message.NewMessage(message.RoleUser, "hi")}
	p := &fakeProvider{turns: []scriptedTurn{{text: "ok"}}}
	a := New(Config{
		Provider:   p,
		Registry:   newRegistry(),
		Prompt:     prompt.New(""),
		WorkDir:    "/tmp/new",
		RefreshEnv: true,
		CollectEnv: fakeEnv("/tmp/new"),
	})
	evs := runLoop(t, a, history, "question")

	msgs := p.history(0)
	if len(msgs) == 0 || !msgs[0].IsSupplement || msgs[0].SupplementKind != message.SupplementEnv || msgs[0].SupplementSeq != 1 {
		t.Fatalf("first message is not env 补充消息: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "/tmp/new") {
		t.Errorf("env 补充消息 not refreshed: %s", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "/old") {
		t.Errorf("old env content leaked: %s", msgs[0].Content)
	}
	if n := countSupplementKind(msgs, message.SupplementEnv); n != 1 {
		t.Errorf("env 补充消息 = %d, want 1", n)
	}

	// EventUserMessage 快照也应包含刷新后的 env 补充消息。
	ev, ok := findEvent(evs, EventUserMessage)
	if !ok || len(ev.Messages) == 0 || !ev.Messages[0].IsSupplement {
		t.Errorf("EventUserMessage snapshot missing env 补充消息: %+v", ev)
	}
}

func TestEnvSupplementMovedToFrontWithoutRefresh(t *testing.T) {
	old := message.NewEnvSupplement("旧环境")
	history := []message.Message{
		message.NewMessage(message.RoleUser, "hi"),
		old,
		message.NewMessage(message.RoleAssistant, "ok"),
	}
	p := &fakeProvider{turns: []scriptedTurn{{text: "done"}}}
	a := New(Config{
		Provider:   p,
		Registry:   newRegistry(),
		Prompt:     prompt.New(""),
		RefreshEnv: false,
		CollectEnv: func(context.Context, string) environment.Info {
			t.Error("collector should not be called when env 补充消息 exists and RefreshEnv=false")
			return environment.Info{}
		},
	})
	runLoop(t, a, history, "next")

	msgs := p.history(0)
	if !msgs[0].IsSupplement || msgs[0].SupplementKind != message.SupplementEnv || !strings.Contains(msgs[0].Content, "旧环境") {
		t.Fatalf("env 补充消息 not moved to front: %+v", msgs)
	}
	if n := countSupplementKind(msgs, message.SupplementEnv); n != 1 {
		t.Errorf("env 补充消息 = %d, want 1", n)
	}
}

func TestEnvSupplementCreatedWhenMissing(t *testing.T) {
	p := &fakeProvider{turns: []scriptedTurn{{text: "done"}}}
	a := New(Config{
		Provider:   p,
		Registry:   newRegistry(),
		Prompt:     prompt.New(""),
		RefreshEnv: false,
		CollectEnv: fakeEnv("/tmp/missing"),
	})
	runLoop(t, a, nil, "hi")
	msgs := p.history(0)
	if len(msgs) == 0 || !msgs[0].IsSupplement || msgs[0].SupplementKind != message.SupplementEnv {
		t.Fatalf("env 补充消息 should be created even without RefreshEnv: %+v", msgs)
	}
}

func TestPlanCadence(t *testing.T) {
	read := &fakeTool{name: "read_file", readOnly: true}
	turns := make([]scriptedTurn, 0, 7)
	for i := 1; i <= 6; i++ {
		turns = append(turns, scriptedTurn{calls: []message.ToolCall{
			call(fmt.Sprintf("t%d", i), "read_file", map[string]any{"path": fmt.Sprintf("f%d", i)}),
		}})
	}
	turns = append(turns, scriptedTurn{text: "done"})

	p := &fakeProvider{turns: turns}
	a := New(Config{
		Provider:   p,
		Registry:   newRegistry(read),
		Prompt:     prompt.New(""),
		PlanMode:   true,
		MaxTurns:   10,
		WorkDir:    "/tmp/plan",
		RefreshEnv: true,
		CollectEnv: fakeEnv("/tmp/plan"),
	})
	runLoop(t, a, nil, "do it")

	if got := p.requests(); got != 7 {
		t.Fatalf("requests = %d, want 7", got)
	}
	wantCadence := []string{
		message.SupplementCadenceFull,  // turn 1
		message.SupplementCadenceBrief, // 2
		message.SupplementCadenceBrief, // 3
		message.SupplementCadenceBrief, // 4
		message.SupplementCadenceBrief, // 5
		message.SupplementCadenceFull,  // 6
		message.SupplementCadenceBrief, // 7
	}
	system := prompt.New("").System()
	for i := 0; i < 7; i++ {
		msgs := p.history(i)
		if len(msgs) == 0 || !msgs[0].IsSupplement || msgs[0].SupplementKind != message.SupplementEnv {
			t.Fatalf("request %d: env 补充消息 not first: %+v", i, msgs)
		}
		if got := lastModeCadence(msgs); got != wantCadence[i] {
			t.Errorf("request %d cadence = %q, want %q", i, got, wantCadence[i])
		}
		if got := p.systemAt(i); got != system {
			t.Errorf("request %d system changed", i)
		}
		// note 不能插在 assistant.tool_use 与 user.tool_result 之间。
		for j, m := range msgs {
			if m.Role == message.RoleAssistant && len(m.ToolCalls) > 0 {
				if j+1 >= len(msgs) {
					t.Fatalf("request %d: assistant tool_use at end", i)
				}
				next := msgs[j+1]
				if next.IsSupplement {
					t.Errorf("request %d: note inserted between tool_use and tool_result", i)
				}
				if len(next.ToolResults) == 0 {
					t.Errorf("request %d: tool_use not followed by tool_result", i)
				}
			}
		}
	}
}

func TestFreshTransitionSupplementSkipsTurnOne(t *testing.T) {
	transition := message.NewModeSupplement(2, true, true, "enter plan")
	history := []message.Message{
		message.NewMessage(message.RoleUser, "old"),
		message.NewMessage(message.RoleAssistant, "ok"),
		transition,
	}
	p := &fakeProvider{turns: []scriptedTurn{{text: "done"}}}
	a := New(Config{
		Provider:   p,
		Registry:   newRegistry(),
		Prompt:     prompt.New(""),
		PlanMode:   true,
		RefreshEnv: true,
		CollectEnv: fakeEnv("/tmp/fresh"),
	})
	runLoop(t, a, history, "continue")

	msgs := p.history(0)
	if n := countSupplementKind(msgs, message.SupplementMode); n != 1 {
		t.Fatalf("mode 补充消息 = %d, want 1 (fresh transition should cover turn 1): %+v", n, msgs)
	}
	lastMode, ok := message.LastModeSupplement(msgs)
	if !ok || !strings.Contains(lastMode.Content, "enter plan") {
		t.Errorf("last mode 补充消息 is not the transition: %+v", msgs)
	}
}

func TestConsumedTransitionSupplementDoesNotSkipTurnOne(t *testing.T) {
	transition := message.NewModeSupplement(2, true, true, "enter plan")
	history := []message.Message{
		transition,
		message.NewMessage(message.RoleAssistant, "old answer"),
	}
	p := &fakeProvider{turns: []scriptedTurn{{text: "done"}}}
	a := New(Config{
		Provider:   p,
		Registry:   newRegistry(),
		Prompt:     prompt.New(""),
		PlanMode:   true,
		RefreshEnv: true,
		CollectEnv: fakeEnv("/tmp/consumed"),
	})
	runLoop(t, a, history, "continue")

	msgs := p.history(0)
	if n := countSupplementKind(msgs, message.SupplementMode); n != 2 {
		t.Fatalf("mode 补充消息 = %d, want 2 (turn 1 full should be re-injected): %+v", n, msgs)
	}
}

func TestUsageAccumulatedWithCache(t *testing.T) {
	u1 := providerUsage(100, 10, 60, 0, true)
	u2 := providerUsage(50, 5, 20, 10, true)
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "read_file", map[string]any{"path": "a"})}, usage: u1},
		{text: "done", usage: u2},
	}}
	a := New(Config{
		Provider:   p,
		Registry:   newRegistry(&fakeTool{name: "read_file", readOnly: true}),
		Prompt:     prompt.New(""),
		CollectEnv: fakeEnv("/tmp/usage"),
	})
	evs := runLoop(t, a, nil, "go")

	done, ok := findEvent(evs, EventDone)
	if !ok {
		t.Fatal("no EventDone")
	}
	u := done.Usage
	if u.InputTokens != 150 || u.OutputTokens != 15 {
		t.Errorf("usage = %+v, want total 150/15", u)
	}
	if u.CacheReadTokens != 80 || u.CacheCreationTokens != 10 || !u.CacheUsageKnown {
		t.Errorf("cache usage = %+v, want read 80 create 10 known", u)
	}
}
