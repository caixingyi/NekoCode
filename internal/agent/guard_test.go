package agent

import (
	"context"
	"testing"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/tool"
)

// ---- 重复调用检测 ----

func TestRepeatDetectorThreshold(t *testing.T) {
	cases := []struct {
		name   string
		rounds [][]message.ToolCall
		want   bool
	}{
		{
			name:   "单次不算重复",
			rounds: [][]message.ToolCall{{call("1", "read_file", map[string]any{"path": "a"})}},
			want:   false,
		},
		{
			name: "连续两次未达阈值",
			rounds: [][]message.ToolCall{
				{call("1", "read_file", map[string]any{"path": "a"})},
				{call("2", "read_file", map[string]any{"path": "a"})},
			},
			want: false,
		},
		{
			name: "连续三次命中",
			rounds: [][]message.ToolCall{
				{call("1", "read_file", map[string]any{"path": "a"})},
				{call("2", "read_file", map[string]any{"path": "a"})},
				{call("3", "read_file", map[string]any{"path": "a"})},
			},
			want: true,
		},
		{
			name: "中间插入不同调用则计数归零",
			rounds: [][]message.ToolCall{
				{call("1", "read_file", map[string]any{"path": "a"})},
				{call("2", "find_files", map[string]any{"pattern": "*.go"})},
				{call("3", "read_file", map[string]any{"path": "a"})},
				{call("4", "read_file", map[string]any{"path": "a"})},
			},
			want: false,
		},
		{
			name: "参数不同不算重复",
			rounds: [][]message.ToolCall{
				{call("1", "read_file", map[string]any{"path": "a"})},
				{call("2", "read_file", map[string]any{"path": "b"})},
				{call("3", "read_file", map[string]any{"path": "c"})},
			},
			want: false,
		},
		{
			name: "同一轮里出现三次也命中",
			rounds: [][]message.ToolCall{{
				call("1", "write_file", map[string]any{"path": "a"}),
				call("2", "write_file", map[string]any{"path": "a"}),
				call("3", "write_file", map[string]any{"path": "a"}),
			}},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newRepeatDetector(3)
			got := false
			for _, round := range c.rounds {
				got = d.feed(round)
			}
			if got != c.want {
				t.Errorf("feed = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCallKeyIgnoresArgOrder(t *testing.T) {
	// 同一个 map 用不同插入顺序构造，encoding/json 会按 key 排序，键必须相同。
	a := call("", "edit_file", map[string]any{"path": "a.go", "old_string": "x", "new_string": "y"})
	b := call("", "edit_file", map[string]any{"new_string": "y", "old_string": "x", "path": "a.go"})
	if callKey(a) != callKey(b) {
		t.Errorf("key order should not matter:\n%s\n%s", callKey(a), callKey(b))
	}

	// 嵌套结构比较。
	c := call("", "t", map[string]any{"opts": map[string]any{"b": 2, "a": 1}})
	d := call("", "t", map[string]any{"opts": map[string]any{"a": 1, "b": 2}})
	if callKey(c) != callKey(d) {
		t.Errorf("nested key order should not matter:\n%s\n%s", callKey(c), callKey(d))
	}

	// 不同工具名不同键。
	if callKey(a) == callKey(call("", "write_file", map[string]any{"path": "a.go"})) {
		t.Error("different tool names must not collide")
	}
}

// ---- 上下文超限 ----

func TestContextLimitStopsLoop(t *testing.T) {
	readTool := &fakeTool{name: "read_file", readOnly: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("t1", "read_file", map[string]any{"path": "a"})}, stop: provider.StopToolUse},
		{text: "不该发生", stop: provider.StopEndTurn},
	}}
	checked := 0
	a := New(Config{
		Provider: p, Registry: newRegistry(readTool),
		ContextLimit: func([]message.Message) bool { checked++; return true },
	})

	evs := runLoop(t, a, nil, "hi")

	if checked != 1 {
		t.Errorf("ContextLimit called %d times, want 1", checked)
	}
	if p.requests() != 1 {
		t.Errorf("provider called %d times, want 1 (should stop before next turn)", p.requests())
	}
	done := lastEvent(evs)
	if done.StopReason != StopContextLimit {
		t.Errorf("StopReason = %q, want context_limit", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

func TestContextLimitNotCheckedWhenLoopEndsNaturally(t *testing.T) {
	p := &fakeProvider{turns: []scriptedTurn{{text: "done", stop: provider.StopEndTurn}}}
	checked := 0
	a := New(Config{
		Provider: p, Registry: newRegistry(),
		ContextLimit: func([]message.Message) bool { checked++; return true },
	})

	runLoop(t, a, nil, "hi")

	if checked != 0 {
		t.Errorf("ContextLimit called %d times on a natural end, want 0", checked)
	}
}

// ---- 三处取消点：历史必须始终配对 ----

// cancelingConfirmer 在等待确认时取消 ctx，并像真实实现那样返回 ctx 的错误。
type cancelingConfirmer struct {
	cancel context.CancelFunc
	calls  int
}

func (c *cancelingConfirmer) Confirm(ctx context.Context, _ message.ToolCall, _ string) (Decision, error) {
	c.calls++
	c.cancel()
	return DecisionDeny, ctx.Err()
}

func TestCancelWhileWaitingForConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	writeTool := &fakeTool{name: "write_file", confirm: true}
	neverRun := &fakeTool{name: "edit_file", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{
			call("w1", "write_file", map[string]any{"path": "a"}),
			call("w2", "edit_file", map[string]any{"path": "b"}),
		}, stop: provider.StopToolUse},
		{text: "不该发生", stop: provider.StopEndTurn},
	}}
	conf := &cancelingConfirmer{cancel: cancel}
	a := New(Config{Provider: p, Registry: newRegistry(writeTool, neverRun), Confirmer: conf})

	var evs []Event
	for ev := range a.Run(ctx, nil, "hi") {
		evs = append(evs, ev)
	}

	if conf.calls != 1 {
		t.Errorf("confirmer called %d times, want 1", conf.calls)
	}
	if writeTool.execCount() != 0 || neverRun.execCount() != 0 {
		t.Error("no write should run when confirmation is canceled")
	}
	done := lastEvent(evs)
	if done.StopReason != StopCanceled {
		t.Errorf("StopReason = %q, want canceled", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

// TestCancelPointsAlwaysKeepHistoryPaired 是三个取消点的对照测试：
// 无论打断发生在流式中、工具执行中还是等待确认中，历史都必须配对。
func TestCancelPointsAlwaysKeepHistoryPaired(t *testing.T) {
	readTool := &fakeTool{name: "read_file", readOnly: true}
	writeTool := &fakeTool{name: "write_file", confirm: true}

	t.Run("流式中", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p := &cancelDuringStreamProvider{cancel: cancel, calls: []message.ToolCall{
			call("t1", "read_file", map[string]any{"path": "a"}),
		}}
		a := New(Config{Provider: p, Registry: newRegistry(readTool)})
		assertRunCanceled(t, a, ctx)
	})

	t.Run("工具执行中", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		canceling := &fakeTool{name: "read_file", readOnly: true, fn: func(context.Context, map[string]any) tool.Result {
			cancel()
			return tool.Result{OK: true, Content: "done"}
		}}
		p := &fakeProvider{turns: []scriptedTurn{
			{calls: []message.ToolCall{
				call("t1", "read_file", map[string]any{"path": "a"}),
				call("t2", "write_file", map[string]any{"path": "b"}),
			}, stop: provider.StopToolUse},
		}}
		a := New(Config{
			Provider: p, Registry: newRegistry(canceling, writeTool),
			Confirmer: &allowConfirmer{},
		})
		assertRunCanceled(t, a, ctx)
	})

	t.Run("等待确认中", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p := &fakeProvider{turns: []scriptedTurn{
			{calls: []message.ToolCall{call("t1", "write_file", map[string]any{"path": "a"})}, stop: provider.StopToolUse},
		}}
		a := New(Config{
			Provider: p, Registry: newRegistry(writeTool),
			Confirmer: &cancelingConfirmer{cancel: cancel},
		})
		assertRunCanceled(t, a, ctx)
	})
}

// assertRunCanceled 跑完循环并断言：收场原因是取消，且历史配对完整。
func assertRunCanceled(t *testing.T, a *Agent, ctx context.Context) {
	t.Helper()
	var evs []Event
	for ev := range a.Run(ctx, nil, "hi") {
		evs = append(evs, ev)
	}
	done := lastEvent(evs)
	if done.StopReason != StopCanceled {
		t.Errorf("StopReason = %q, want canceled", done.StopReason)
	}
	assertPaired(t, done.Messages)
	if got := countEvents(evs, EventDone); got != 1 {
		t.Errorf("EventDone count = %d, want exactly 1", got)
	}
}

// ---- 取消后能否接着对话 ----

// TestCanceledHistoryCanContinue：取消产生的历史必须是「可继续」的——
// 既能让两家 provider 归一化通过（Anthropic 对 tool_use/tool_result 配对的硬约束），
// 也能直接作为下一次 Run 的上下文（AC4）。
func TestCanceledHistoryCanContinue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readTool := &fakeTool{name: "read_file", readOnly: true, fn: func(context.Context, map[string]any) tool.Result {
		cancel()
		return tool.Result{OK: true, Content: "done"}
	}}
	writeTool := &fakeTool{name: "write_file", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{
			call("t1", "read_file", map[string]any{"path": "a"}),
			call("t2", "write_file", map[string]any{"path": "b"}),
		}, stop: provider.StopToolUse},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(readTool, writeTool), Confirmer: &allowConfirmer{}})

	var evs []Event
	for ev := range a.Run(ctx, nil, "hi") {
		evs = append(evs, ev)
	}
	hist := lastEvent(evs).Messages
	assertPaired(t, hist)

	// 两家协议都必须能把这个历史归一化成请求体，且不报错。
	for _, protocol := range []string{config.ProtocolAnthropic, config.ProtocolOpenAI} {
		body, err := message.Normalize(config.Profile{Protocol: protocol, Model: "m"}, "sys", nil, hist)
		if err != nil {
			t.Fatalf("Normalize(%s) 失败: %v", protocol, err)
		}
		if body == nil {
			t.Fatalf("Normalize(%s) 返回 nil", protocol)
		}
	}

	// 接着用这段历史发起新循环，应当正常结束。
	p2 := &fakeProvider{turns: []scriptedTurn{{text: "好的，继续", stop: provider.StopEndTurn}}}
	a2 := New(Config{Provider: p2, Registry: newRegistry(readTool, writeTool)})
	var evs2 []Event
	for ev := range a2.Run(context.Background(), hist, "继续") {
		evs2 = append(evs2, ev)
	}
	done := lastEvent(evs2)
	if done.StopReason != StopEndTurn {
		t.Errorf("续跑 StopReason = %q, want end_turn", done.StopReason)
	}
	assertPaired(t, done.Messages)
}

// ---- 事件流契约 ----

// TestEventStreamMetadata：事件类型齐全，且携带 R3 要求的元信息。
func TestEventStreamMetadata(t *testing.T) {
	readTool := &fakeTool{name: "read_file", readOnly: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{
			thinking: "我先看看", signature: "sig-1", text: "读一下",
			calls: []message.ToolCall{call("t1", "read_file", map[string]any{"path": "a"})},
			stop:  provider.StopToolUse,
			usage: &provider.Usage{InputTokens: 10, OutputTokens: 5},
		},
		{text: "done", stop: provider.StopEndTurn, usage: &provider.Usage{InputTokens: 20, OutputTokens: 8}},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(readTool)})

	evs := runLoop(t, a, nil, "看看 a")

	if evs[0].Type != EventUserMessage {
		t.Errorf("首个事件应为 UserMessage，得到 %v", evs[0].Type)
	}
	if evs[0].Text != "看看 a" || len(evs[0].Messages) == 0 {
		t.Errorf("UserMessage 应带回显文本与快照: %+v", evs[0])
	}

	// thinking 增量与签名。
	var sawThinking, sawSignature bool
	for _, ev := range evs {
		if ev.Type == EventThinkingDelta && ev.Text == "我先看看" {
			sawThinking = true
		}
		if ev.Type == EventThinkingDelta && ev.Signature == "sig-1" {
			sawSignature = true
		}
	}
	if !sawThinking || !sawSignature {
		t.Errorf("thinking 增量或签名缺失 (thinking=%v signature=%v)", sawThinking, sawSignature)
	}

	// 工具事件：轮次、批次、是否需要确认、耗时。
	start, ok := findEvent(evs, EventToolCallStart)
	if !ok {
		t.Fatal("缺少 EventToolCallStart")
	}
	if start.Turn != 1 || start.Batch != readBatchNo || start.NeedsConfirm {
		t.Errorf("ToolCallStart 元信息 = %+v", start)
	}
	res, ok := findEvent(evs, EventToolResult)
	if !ok {
		t.Fatal("缺少 EventToolResult")
	}
	if res.Turn != 1 || res.Batch != readBatchNo || res.Result.ID != "t1" {
		t.Errorf("ToolResult 元信息 = %+v", res)
	}
	if res.Duration <= 0 {
		t.Errorf("ToolResult 应带执行耗时，得到 %v", res.Duration)
	}

	// 轮次事件与结束事件。
	var turnEnds []Event
	for _, ev := range evs {
		if ev.Type == EventTurnEnd {
			turnEnds = append(turnEnds, ev)
		}
	}
	if len(turnEnds) != 2 {
		t.Fatalf("TurnEnd 数量 = %d, want 2", len(turnEnds))
	}
	if turnEnds[0].Turn != 1 || turnEnds[0].StopReason != StopReason(provider.StopToolUse) {
		t.Errorf("第一轮 TurnEnd = %+v", turnEnds[0])
	}
	if turnEnds[0].Usage.InputTokens != 10 || turnEnds[0].Usage.OutputTokens != 5 {
		t.Errorf("TurnEnd 本轮用量 = %+v", turnEnds[0].Usage)
	}
	if len(turnEnds[0].Messages) == 0 {
		t.Error("TurnEnd 应带历史快照")
	}

	done := lastEvent(evs)
	if done.Type != EventDone || done.Turn != 2 || done.StopReason != StopEndTurn {
		t.Errorf("Done = %+v", done)
	}
	// 累计用量 = 两轮之和。
	if done.Usage.InputTokens != 30 || done.Usage.OutputTokens != 13 {
		t.Errorf("累计用量 = %+v, want {30 13}", done.Usage)
	}
}
