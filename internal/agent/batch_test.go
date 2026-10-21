package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/tool"
)

// ---- 并发观测工具 ----

// barrier 返回一个「n 个调用者都到达后才一起放行」的闸门。
// 用它证明并发：若工具被串行执行，第 n 个永远到不了，调用方会一直阻塞。
func newBarrier(n int) func() {
	var once sync.Once
	arrived := make(chan struct{}, n)
	release := make(chan struct{})
	return func() {
		arrived <- struct{}{}
		if len(arrived) >= n {
			once.Do(func() { close(release) })
		}
		<-release
	}
}

// execLog 按开始顺序记录工具执行，并统计并发峰值。
type execLog struct {
	mu      sync.Mutex
	order   []string
	current int
	max     int
}

func (l *execLog) enter(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.order = append(l.order, name)
	l.current++
	if l.current > l.max {
		l.max = l.current
	}
}

func (l *execLog) leave() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current--
}

func (l *execLog) snapshot() ([]string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.order...), l.max
}

// loggedTool 是执行时记账的读工具。
func loggedTool(name string, log *execLog, gate func()) *fakeTool {
	return &fakeTool{name: name, readOnly: true, fn: func(context.Context, map[string]any) tool.Result {
		log.enter(name)
		defer log.leave()
		if gate != nil {
			gate()
		}
		return tool.Result{OK: true, Content: name + " ok"}
	}}
}

// toolResultsOf 取出历史里第一条工具结果消息（回灌的 user 消息）。
func toolResultsOf(t *testing.T, msgs []message.Message) []message.ToolResult {
	t.Helper()
	for _, m := range msgs {
		if len(m.ToolResults) > 0 {
			return m.ToolResults
		}
	}
	t.Fatal("history has no tool results")
	return nil
}

// runWithTimeout 跑完循环，超时则判失败（并发闸门卡住时用来给出清晰报错）。
func runWithTimeout(t *testing.T, a *Agent, input string, d time.Duration) []Event {
	t.Helper()
	done := make(chan []Event, 1)
	go func() {
		var evs []Event
		for ev := range a.Run(context.Background(), nil, input) {
			evs = append(evs, ev)
		}
		done <- evs
	}()
	select {
	case evs := <-done:
		return evs
	case <-time.After(d):
		t.Fatal("loop did not finish in time (tools likely ran serially)")
		return nil
	}
}

// ---- 测试 ----

// TestReadsRunConcurrently：三个读工具必须同时进入执行，否则闸门不会放行（R10）。
func TestReadsRunConcurrently(t *testing.T) {
	const n = 3
	gate := newBarrier(n)
	log := &execLog{}
	tools := []tool.Tool{
		loggedTool("read_a", log, gate),
		loggedTool("read_b", log, gate),
		loggedTool("read_c", log, gate),
	}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{
			call("1", "read_a", map[string]any{"path": "a"}),
			call("2", "read_b", map[string]any{"path": "b"}),
			call("3", "read_c", map[string]any{"path": "c"}),
		}, stop: provider.StopToolUse},
		{text: "done", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(tools...)})

	evs := runWithTimeout(t, a, "read all", 3*time.Second)

	_, max := log.snapshot()
	if max != n {
		t.Errorf("max concurrency = %d, want %d", max, n)
	}
	if got := countEvents(evs, EventToolResult); got != n {
		t.Errorf("tool results = %d, want %d", got, n)
	}
}

// TestMaxParallelRespected：并发上限是硬约束，任何时刻同时在跑的工具不超过它（R10）。
func TestMaxParallelRespected(t *testing.T) {
	log := &execLog{}
	tools := []tool.Tool{
		loggedTool("read_a", log, nil),
		loggedTool("read_b", log, nil),
		loggedTool("read_c", log, nil),
		loggedTool("read_d", log, nil),
	}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{
			call("1", "read_a", map[string]any{"path": "a"}),
			call("2", "read_b", map[string]any{"path": "b"}),
			call("3", "read_c", map[string]any{"path": "c"}),
			call("4", "read_d", map[string]any{"path": "d"}),
		}, stop: provider.StopToolUse},
		{text: "done", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(tools...), MaxParallel: 2})

	runWithTimeout(t, a, "read all", 3*time.Second)

	_, max := log.snapshot()
	if max > 2 {
		t.Errorf("max concurrency = %d, want <= 2", max)
	}
}

// TestMixedBatchRunsReadsBeforeWrites：读批先整体跑完，写批随后按原始顺序串行（R11）。
func TestMixedBatchRunsReadsBeforeWrites(t *testing.T) {
	log := &execLog{}
	w1 := &fakeTool{name: "write_file", confirm: true, fn: func(context.Context, map[string]any) tool.Result {
		log.enter("w1")
		defer log.leave()
		return tool.Result{OK: true, Content: "w1 ok"}
	}}
	w2 := &fakeTool{name: "edit_file", confirm: true, fn: func(context.Context, map[string]any) tool.Result {
		log.enter("w2")
		defer log.leave()
		return tool.Result{OK: true, Content: "w2 ok"}
	}}
	reg := newRegistry(loggedTool("read_a", log, nil), loggedTool("read_b", log, nil), w1, w2)

	// 模型给出的顺序刻意把写夹在中间，验证调度重排 + 结果保序。
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{
			call("w1", "write_file", map[string]any{"path": "a"}),
			call("r1", "read_a", map[string]any{"path": "b"}),
			call("r2", "read_b", map[string]any{"path": "c"}),
			call("w2", "edit_file", map[string]any{"path": "d"}),
		}, stop: provider.StopToolUse},
		{text: "done", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: reg, Confirmer: &allowConfirmer{}})

	evs := runWithTimeout(t, a, "mixed", 3*time.Second)

	order, _ := log.snapshot()
	if len(order) != 4 {
		t.Fatalf("execution order = %v, want 4 entries", order)
	}
	for _, name := range order[:2] {
		if name != "read_a" && name != "read_b" {
			t.Errorf("read batch should run first, order = %v", order)
		}
	}
	if order[2] != "w1" || order[3] != "w2" {
		t.Errorf("writes should run last in original order, order = %v", order)
	}

	// 回填顺序必须等于模型给出的顺序。
	done := lastEvent(evs)
	results := toolResultsOf(t, done.Messages)
	wantIDs := []string{"w1", "r1", "r2", "w2"}
	if len(results) != len(wantIDs) {
		t.Fatalf("results = %d, want %d", len(results), len(wantIDs))
	}
	for i, id := range wantIDs {
		if results[i].ID != id {
			t.Errorf("result[%d].ID = %s, want %s (回填必须保序)", i, results[i].ID, id)
		}
	}
}

// TestDeniedWriteSkipsRemainingWrites：拒绝一个写操作后，本批剩余写类全部跳过但结果齐全（R13）。
func TestDeniedWriteSkipsRemainingWrites(t *testing.T) {
	read := &fakeTool{name: "read_a", readOnly: true}
	w1 := &fakeTool{name: "write_file", confirm: true}
	w2 := &fakeTool{name: "edit_file", confirm: true}
	w3 := &fakeTool{name: "run_command", confirm: true}

	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{
			call("r1", "read_a", map[string]any{"path": "a"}),
			call("w1", "write_file", map[string]any{"path": "b"}),
			call("w2", "edit_file", map[string]any{"path": "c"}),
			call("w3", "run_command", map[string]any{"command": "ls"}),
		}, stop: provider.StopToolUse},
		{text: "好吧", stop: provider.StopEndTurn},
	}}
	a := New(Config{
		Provider: p, Registry: newRegistry(read, w1, w2, w3),
		Confirmer: &denyConfirmer{},
	})

	evs := runWithTimeout(t, a, "改三处", 3*time.Second)

	if read.execCount() != 1 {
		t.Errorf("read batch must be unaffected by the denial, ran %d times", read.execCount())
	}
	if w1.execCount()+w2.execCount()+w3.execCount() != 0 {
		t.Errorf("no write should run: w1=%d w2=%d w3=%d", w1.execCount(), w2.execCount(), w3.execCount())
	}
	if got := countEvents(evs, EventToolRejected); got != 1 {
		t.Errorf("ToolRejected = %d, want 1 (只有被问到的那一个)", got)
	}
	if done := lastEvent(evs); done.StopReason != StopEndTurn {
		t.Errorf("loop should continue after denial, got %q", done.StopReason)
	}

	done := lastEvent(evs)
	results := toolResultsOf(t, done.Messages)
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4", len(results))
	}
	if !strings.Contains(results[1].Content, "拒绝") {
		t.Errorf("w1 result = %q, want 拒绝", results[1].Content)
	}
	if !strings.Contains(results[2].Content, "跳过") || !strings.Contains(results[3].Content, "跳过") {
		t.Errorf("w2/w3 should be skipped: %q / %q", results[2].Content, results[3].Content)
	}
	assertPaired(t, done.Messages)
}

// denyConfirmer 一律拒绝。
type denyConfirmer struct{ calls int }

func (c *denyConfirmer) Confirm(context.Context, message.ToolCall, string) (Decision, error) {
	c.calls++
	return DecisionDeny, nil
}
