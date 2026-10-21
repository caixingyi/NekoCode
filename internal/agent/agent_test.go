package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/tool"
)

// ---- 测试替身 ----

// scriptedTurn 是一轮模型的预设产出。
type scriptedTurn struct {
	text      string
	thinking  string
	signature string
	calls     []message.ToolCall
	stop      string
	usage     *provider.Usage
	err       error
}

// fakeProvider 按顺序回放预设轮次；请求历史被记录下来供断言。
// 轮次用尽后重复最后一轮（配合轮数上限/重复检测使用）。
type fakeProvider struct {
	turns []scriptedTurn

	mu   sync.Mutex
	n    int
	seen [][]message.Message
}

func (f *fakeProvider) Stream(_ context.Context, _ string, _ []tool.Definition, msgs []message.Message) <-chan provider.StreamEvent {
	f.mu.Lock()
	idx := f.n
	f.n++
	if idx >= len(f.turns) {
		idx = len(f.turns) - 1
	}
	f.seen = append(f.seen, cloneMessages(msgs))
	f.mu.Unlock()

	ch := make(chan provider.StreamEvent, 16)
	go func() {
		defer close(ch)
		if idx < 0 {
			ch <- provider.StreamEvent{Type: provider.EventDone, StopReason: provider.StopEndTurn}
			return
		}
		t := f.turns[idx]
		if t.err != nil {
			ch <- provider.StreamEvent{Type: provider.EventError, Err: t.err}
			return
		}
		if t.thinking != "" {
			ch <- provider.StreamEvent{Type: provider.EventThinking, Text: t.thinking}
		}
		if t.signature != "" {
			ch <- provider.StreamEvent{Type: provider.EventThinking, Signature: t.signature}
		}
		if t.text != "" {
			ch <- provider.StreamEvent{Type: provider.EventText, Text: t.text}
		}
		for _, c := range t.calls {
			ch <- provider.StreamEvent{Type: provider.EventToolCall, ToolCall: c}
		}
		ch <- provider.StreamEvent{Type: provider.EventDone, StopReason: t.stop, Usage: t.usage}
	}()
	return ch
}

func (f *fakeProvider) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func (f *fakeProvider) history(i int) []message.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[i]
}

// fakeTool 是可编程的工具替身，execCount 用原子计数（并发批次测试会用到）。
type fakeTool struct {
	name     string
	confirm  bool
	readOnly bool
	fn       func(ctx context.Context, args map[string]any) tool.Result
	execN    int32
}

func (f *fakeTool) Name() string           { return f.name }
func (f *fakeTool) Description() string    { return "fake " + f.name }
func (f *fakeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (f *fakeTool) NeedsConfirm() bool     { return f.confirm }
func (f *fakeTool) IsReadOnly() bool       { return f.readOnly }
func (f *fakeTool) execCount() int         { return int(atomic.LoadInt32(&f.execN)) }
func (f *fakeTool) Execute(ctx context.Context, args map[string]any) tool.Result {
	atomic.AddInt32(&f.execN, 1)
	if f.fn == nil {
		return tool.Result{OK: true, Content: f.name + " ok"}
	}
	return f.fn(ctx, args)
}

// ---- 辅助 ----

func newRegistry(tools ...tool.Tool) *tool.Registry {
	r := tool.NewRegistry(".")
	for _, tl := range tools {
		r.Register(tl)
	}
	return r
}

// call 构造一次工具调用。
func call(id, name string, input map[string]any) message.ToolCall {
	return message.ToolCall{ID: id, Name: name, Input: input}
}

// runLoop 跑完一次循环并收集全部事件。
func runLoop(t *testing.T, a *Agent, history []message.Message, input string) []Event {
	t.Helper()
	var evs []Event
	for ev := range a.Run(context.Background(), history, input) {
		evs = append(evs, ev)
	}
	return evs
}

func findEvent(evs []Event, typ EventType) (Event, bool) {
	for _, ev := range evs {
		if ev.Type == typ {
			return ev, true
		}
	}
	return Event{}, false
}

func lastEvent(evs []Event) Event { return evs[len(evs)-1] }

func countEvents(evs []Event, typ EventType) int {
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// assertPaired 断言历史里每个 tool_use 都有配对的 tool_result
// （Anthropic 的硬约束，缺了下次请求会被拒）。
func assertPaired(t *testing.T, msgs []message.Message) {
	t.Helper()
	pending := map[string]bool{}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			pending[c.ID] = true
		}
		for _, r := range m.ToolResults {
			if !pending[r.ID] {
				t.Errorf("tool_result %s 没有对应的 tool_use", r.ID)
				continue
			}
			delete(pending, r.ID)
		}
	}
	for id := range pending {
		t.Errorf("tool_use %s 没有配对的 tool_result", id)
	}
}

// ---- 测试 ----

func TestLoopSingleTurnNoTools(t *testing.T) {
	p := &fakeProvider{turns: []scriptedTurn{{text: "你好", stop: provider.StopEndTurn}}}
	a := New(Config{Provider: p, Registry: newRegistry()})

	evs := runLoop(t, a, nil, "hi")

	if evs[0].Type != EventUserMessage || evs[0].Text != "hi" {
		t.Errorf("first event = %+v, want user message", evs[0])
	}
	if got := countEvents(evs, EventTextDelta); got != 1 {
		t.Errorf("TextDelta count = %d, want 1", got)
	}
	turnEnd, ok := findEvent(evs, EventTurnEnd)
	if !ok {
		t.Fatal("missing TurnEnd")
	}
	if turnEnd.StopReason != StopEndTurn || turnEnd.Turn != 1 {
		t.Errorf("TurnEnd = %+v", turnEnd)
	}
	done := lastEvent(evs)
	if done.Type != EventDone || done.StopReason != StopEndTurn {
		t.Errorf("last event = %+v, want Done/end_turn", done)
	}
	if len(done.Messages) != 2 {
		t.Fatalf("history = %d messages, want user+assistant", len(done.Messages))
	}
	if done.Messages[1].Content != "你好" {
		t.Errorf("assistant content = %q", done.Messages[1].Content)
	}
	assertPaired(t, done.Messages)
}

func TestLoopMultiTurnWithTool(t *testing.T) {
	readTool := &fakeTool{name: "read_file", readOnly: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "read_file", map[string]any{"path": "a.go"})}, stop: provider.StopToolUse},
		{text: "读完了", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(readTool)})

	evs := runLoop(t, a, nil, "看看 a.go")

	if readTool.execCount() != 1 {
		t.Errorf("read_file executed %d times, want 1", readTool.execCount())
	}
	if got := countEvents(evs, EventTurnEnd); got != 2 {
		t.Errorf("TurnEnd count = %d, want 2", got)
	}
	if got := countEvents(evs, EventToolCallStart); got != 1 {
		t.Errorf("ToolCallStart count = %d, want 1", got)
	}
	res, _ := findEvent(evs, EventToolResult)
	if res.Result.ID != "t1" || res.Result.IsError {
		t.Errorf("tool result = %+v", res.Result)
	}

	// 第二次请求的历史里，tool_use 后面必须紧跟配对的 tool_result。
	second := p.history(1)
	if len(second) != 3 {
		t.Fatalf("second request history = %d messages, want user+assistant+results", len(second))
	}
	if len(second[1].ToolCalls) != 1 || len(second[2].ToolResults) != 1 {
		t.Errorf("history pairing wrong: %+v", second)
	}
	done := lastEvent(evs)
	if done.StopReason != StopEndTurn {
		t.Errorf("StopReason = %q, want end_turn", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

func TestLoopMaxTurns(t *testing.T) {
	// 每轮都要调工具，永不自然结束 → 必须被轮数上限截断。
	// 每轮参数不同，避免被重复检测先一步拦下。
	readTool := &fakeTool{name: "read_file", readOnly: true}
	mk := func(id, path string) scriptedTurn {
		return scriptedTurn{
			calls: []message.ToolCall{call(id, "read_file", map[string]any{"path": path})},
			stop:  provider.StopToolUse,
		}
	}
	p := &fakeProvider{turns: []scriptedTurn{mk("t1", "a.go"), mk("t2", "b.go"), mk("t3", "c.go")}}
	a := New(Config{Provider: p, Registry: newRegistry(readTool), MaxTurns: 3})

	evs := runLoop(t, a, nil, "loop")

	if p.requests() != 3 {
		t.Errorf("provider called %d times, want 3", p.requests())
	}
	done := lastEvent(evs)
	if done.StopReason != StopMaxTurns {
		t.Errorf("StopReason = %q, want max_turns", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

func TestLoopMaxTokensTruncation(t *testing.T) {
	// 输出被截断：即使带着工具调用也不执行，改为补齐结果后收场（R7）。
	writeTool := &fakeTool{name: "write_file", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "write_file", map[string]any{"path": "a"})}, stop: provider.StopMaxTokens},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(writeTool), Confirmer: &allowConfirmer{}})

	evs := runLoop(t, a, nil, "write")

	if writeTool.execCount() != 0 {
		t.Errorf("truncated turn should not execute tools, ran %d times", writeTool.execCount())
	}
	done := lastEvent(evs)
	if done.StopReason != StopMaxTokens {
		t.Errorf("StopReason = %q, want max_tokens", done.StopReason)
	}
	assertPaired(t, done.Messages)
	var found bool
	for _, m := range done.Messages {
		for _, r := range m.ToolResults {
			if strings.Contains(r.Content, "截断") {
				found = true
			}
		}
	}
	if !found {
		t.Error("truncated tool call should get a synthesized result")
	}
}

func TestLoopEmptyResponse(t *testing.T) {
	p := &fakeProvider{turns: []scriptedTurn{{stop: provider.StopEndTurn}}}
	a := New(Config{Provider: p, Registry: newRegistry()})

	evs := runLoop(t, a, nil, "hi")
	done := lastEvent(evs)
	if done.StopReason != StopEmptyResponse {
		t.Errorf("StopReason = %q, want empty_response", done.StopReason)
	}
	if len(done.Messages) != 1 {
		t.Errorf("empty assistant message should not be stored, history = %+v", done.Messages)
	}
}

func TestLoopUpstreamError(t *testing.T) {
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "read_file", map[string]any{"path": "a"})}, err: errors.New("boom")},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(&fakeTool{name: "read_file", readOnly: true})})

	evs := runLoop(t, a, nil, "hi")

	if _, ok := findEvent(evs, EventError); !ok {
		t.Error("missing Error event")
	}
	done := lastEvent(evs)
	if done.StopReason != StopError {
		t.Errorf("StopReason = %q, want error", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

func TestLoopCancelMidToolBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// 读批里唯一的工具执行时取消，随后的写批必须整体跳过（串行批才可能「还没轮到」）（R9）。
	canceling := &fakeTool{name: "read_file", readOnly: true, fn: func(context.Context, map[string]any) tool.Result {
		cancel()
		return tool.Result{OK: true, Content: "done"}
	}}
	neverRun := &fakeTool{name: "write_file", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{
			call("t1", "read_file", map[string]any{"path": "a"}),
			call("t2", "write_file", map[string]any{"path": "b"}),
		}, stop: provider.StopToolUse},
		{text: "不该发生", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(canceling, neverRun), Confirmer: &allowConfirmer{}})

	var evs []Event
	for ev := range a.Run(ctx, nil, "hi") {
		evs = append(evs, ev)
	}

	if neverRun.execCount() != 0 {
		t.Errorf("write tool should be skipped after cancel, ran %d times", neverRun.execCount())
	}
	if p.requests() != 1 {
		t.Errorf("provider called %d times after cancel, want 1", p.requests())
	}
	done := lastEvent(evs)
	if done.StopReason != StopCanceled {
		t.Errorf("StopReason = %q, want canceled", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

func TestLoopCancelBeforeAnyTool(t *testing.T) {
	// 取消发生在流式阶段：已收到的 tool_use 也必须补齐结果。
	ctx, cancel := context.WithCancel(context.Background())
	readTool := &fakeTool{name: "read_file", readOnly: true}
	p := &cancelDuringStreamProvider{cancel: cancel, calls: []message.ToolCall{
		call("t1", "read_file", map[string]any{"path": "a"}),
	}}
	a := New(Config{Provider: p, Registry: newRegistry(readTool)})

	var evs []Event
	for ev := range a.Run(ctx, nil, "hi") {
		evs = append(evs, ev)
	}

	if readTool.execCount() != 0 {
		t.Errorf("no tool should run after cancel, ran %d times", readTool.execCount())
	}
	done := lastEvent(evs)
	if done.StopReason != StopCanceled {
		t.Errorf("StopReason = %q, want canceled", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

// cancelDuringStreamProvider 在发出 tool_use 之后取消 ctx，模拟流式过程中被打断。
type cancelDuringStreamProvider struct {
	cancel context.CancelFunc
	calls  []message.ToolCall
}

func (c *cancelDuringStreamProvider) Stream(context.Context, string, []tool.Definition, []message.Message) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 16)
	go func() {
		defer close(ch)
		for _, tc := range c.calls {
			ch <- provider.StreamEvent{Type: provider.EventToolCall, ToolCall: tc}
		}
		c.cancel()
	}()
	return ch
}

func TestLoopRepeatCallStops(t *testing.T) {
	// 同一个调用连续出现 → 第 3 轮开始前判定中止，工具只跑了 2 次。
	readTool := &fakeTool{name: "read_file", readOnly: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "read_file", map[string]any{"path": "a"})}, stop: provider.StopToolUse},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(readTool)})

	evs := runLoop(t, a, nil, "loop")

	if readTool.execCount() != 2 {
		t.Errorf("tool ran %d times, want 2 before repeat detection", readTool.execCount())
	}
	done := lastEvent(evs)
	if done.StopReason != StopRepeatCall {
		t.Errorf("StopReason = %q, want repeat_call", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

func TestLoopAlternatingCallsNotFlagged(t *testing.T) {
	// 读-写-读 交替不算重复（参数不同），不应被误杀。
	read1 := &fakeTool{name: "read_file", readOnly: true}
	read2 := &fakeTool{name: "find_files", readOnly: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("a", "read_file", map[string]any{"path": "x"})}, stop: provider.StopToolUse},
		{calls: []message.ToolCall{call("b", "find_files", map[string]any{"pattern": "*.go"})}, stop: provider.StopToolUse},
		{calls: []message.ToolCall{call("c", "read_file", map[string]any{"path": "x"})}, stop: provider.StopToolUse},
		{text: "ok", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(read1, read2)})

	evs := runLoop(t, a, nil, "mix")
	if done := lastEvent(evs); done.StopReason != StopEndTurn {
		t.Errorf("StopReason = %q, want end_turn (alternating calls are not repeats)", done.StopReason)
	}
}

func TestLoopUnknownTool(t *testing.T) {
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "nope", map[string]any{})}, stop: provider.StopToolUse},
		{text: "改用别的办法", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry()})

	evs := runLoop(t, a, nil, "hi")
	res, _ := findEvent(evs, EventToolResult)
	if !res.Result.IsError || !strings.Contains(res.Result.Content, "未知工具") {
		t.Errorf("unknown tool result = %+v", res.Result)
	}
	if done := lastEvent(evs); done.StopReason != StopEndTurn {
		t.Errorf("loop should continue after unknown tool, got %q", done.StopReason)
	}
}

// allowConfirmer 是「一律允许」的确认器替身。
type allowConfirmer struct{ calls int }

func (c *allowConfirmer) Confirm(context.Context, message.ToolCall, string) (Decision, error) {
	c.calls++
	return DecisionAllow, nil
}

func TestWriteToolWithoutConfirmerIsDenied(t *testing.T) {
	// 没有确认通道时写类工具按安全默认拒绝（决策 3）。
	writeTool := &fakeTool{name: "write_file", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "write_file", map[string]any{"path": "a"})}, stop: provider.StopToolUse},
		{text: "好", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(writeTool)})

	evs := runLoop(t, a, nil, "write")
	if writeTool.execCount() != 0 {
		t.Error("write tool must not run without a confirmer")
	}
	res, _ := findEvent(evs, EventToolResult)
	if !res.Result.IsError || !strings.Contains(res.Result.Content, "确认通道") {
		t.Errorf("result = %+v, want safety-default denial", res.Result)
	}
}

func TestAllowAllSkipsLaterConfirmations(t *testing.T) {
	writeTool := &fakeTool{name: "write_file", confirm: true}
	conf := &allowAllConfirmer{}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "write_file", map[string]any{"path": "a"})}, stop: provider.StopToolUse},
		{calls: []message.ToolCall{call("t2", "write_file", map[string]any{"path": "b"})}, stop: provider.StopToolUse},
		{text: "done", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(writeTool), Confirmer: conf})

	runLoop(t, a, nil, "write twice")

	if conf.calls != 1 {
		t.Errorf("confirmer called %d times, want 1 (allow-all should persist)", conf.calls)
	}
	if writeTool.execCount() != 2 {
		t.Errorf("write tool ran %d times, want 2", writeTool.execCount())
	}
}

type allowAllConfirmer struct{ calls int }

func (c *allowAllConfirmer) Confirm(context.Context, message.ToolCall, string) (Decision, error) {
	c.calls++
	return DecisionAllowAll, nil
}

func TestDescribePreview(t *testing.T) {
	cases := []struct {
		name string
		call message.ToolCall
		want string
	}{
		{"run", call("", "run_command", map[string]any{"command": "go test"}), "$ go test"},
		{"edit", call("", "edit_file", map[string]any{"path": "a.go", "old_string": "x", "new_string": "y"}), "编辑 a.go\n- x\n+ y"},
	}
	for _, c := range cases {
		if got := Describe(c.call); got != c.want {
			t.Errorf("%s: Describe = %q, want %q", c.name, got, c.want)
		}
	}
	if got := Describe(call("", "write_file", map[string]any{"path": "a.go", "content": "hi"})); !strings.Contains(got, "写入 a.go") {
		t.Errorf("write preview = %q", got)
	}
}
