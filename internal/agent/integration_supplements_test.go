package agent

import (
	"context"
	"fmt"
	"testing"

	"nekocode/internal/config"
	aictx "nekocode/internal/context"
	"nekocode/internal/message"
	"nekocode/internal/prompt"
)

// TestSupplementsSurviveSummarize 串起 agent 与 context：
// 多轮 plan 运行产生 env/mode 补充消息 后，压缩结果仍以 env 补充消息 开头，
// summary 补充消息 紧随其后，工具配对不丢，稳定 system 不因轮次变化。
func TestSupplementsSurviveSummarize(t *testing.T) {
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
		RefreshEnv: true,
		CollectEnv: fakeEnv("/tmp/integration"),
	})
	evs := runLoop(t, a, nil, "go")
	done, ok := findEvent(evs, EventDone)
	if !ok {
		t.Fatal("no EventDone")
	}
	history := done.Messages
	if len(history) == 0 || !history[0].IsSupplement || history[0].SupplementKind != message.SupplementEnv {
		t.Fatalf("history does not start with env 补充消息: %+v", history)
	}

	system := a.cfg.Prompt.System()
	for i := 0; i < p.requests(); i++ {
		if got := p.systemAt(i); got != system {
			t.Errorf("request %d system changed", i)
		}
	}

	sp := &fakeProvider{turns: []scriptedTurn{{text: "摘要"}}}
	out, n, err := aictx.Summarize(context.Background(), sp, system,
		config.Profile{Model: "claude-x", ContextWindow: 100}, history, true)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected some messages to be compressed")
	}
	if len(out) < 3 {
		t.Fatalf("len(out) = %d, want env + summary + recent", len(out))
	}
	if !out[0].IsSupplement || out[0].SupplementKind != message.SupplementEnv {
		t.Errorf("env 补充消息 not preserved first: %+v", out[0])
	}
	if out[1].SupplementKind != message.SupplementSummary {
		t.Errorf("summary 补充消息 not second: %+v", out[1])
	}
	assertPaired(t, out)
}
