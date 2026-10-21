package agent

import (
	"context"
	"strings"
	"testing"

	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/tool"
)

// TestPlanModeBlocksWritesButAllowsReads：计划模式下读类照常、写类被拦截（R15）。
func TestPlanModeBlocksWritesButAllowsReads(t *testing.T) {
	read := &fakeTool{name: "read_file", readOnly: true}
	write := &fakeTool{name: "write_file", confirm: true}
	conf := &allowConfirmer{}

	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{
			call("r1", "read_file", map[string]any{"path": "a"}),
			call("w1", "write_file", map[string]any{"path": "b"}),
		}, stop: provider.StopToolUse},
		{text: "这是我的计划：……", stop: provider.StopEndTurn},
	}}
	a := New(Config{
		Provider: p, Registry: newRegistry(read, write),
		Confirmer: conf, PlanMode: true,
	})

	evs := runLoop(t, a, nil, "先给个计划")

	if read.execCount() != 1 {
		t.Errorf("read should still run in plan mode, ran %d times", read.execCount())
	}
	if write.execCount() != 0 {
		t.Error("write must not run in plan mode")
	}
	if conf.calls != 0 {
		t.Errorf("plan mode should block before asking, confirmer called %d times", conf.calls)
	}
	if got := countEvents(evs, EventToolBlocked); got != 1 {
		t.Errorf("ToolBlocked = %d, want 1", got)
	}
	if done := lastEvent(evs); done.StopReason != StopEndTurn {
		t.Errorf("plan should end normally, got %q", done.StopReason)
	}
}

// TestPlanModeBlockNoteTellsModelToStopRetrying：拦截文案必须明确要求输出计划、不要重试。
func TestPlanModeBlockNoteTellsModelToStopRetrying(t *testing.T) {
	write := &fakeTool{name: "write_file", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("w1", "write_file", map[string]any{"path": "a"})}, stop: provider.StopToolUse},
		{text: "计划如下", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(write), PlanMode: true})

	evs := runLoop(t, a, nil, "改一下")

	res, _ := findEvent(evs, EventToolResult)
	if !res.Result.IsError {
		t.Error("blocked result should be marked as error")
	}
	for _, want := range []string{"计划模式", "不要重试", "计划"} {
		if !strings.Contains(res.Result.Content, want) {
			t.Errorf("block note should mention %q, got %q", want, res.Result.Content)
		}
	}
	if !strings.Contains(res.Result.Content, "write_file") {
		t.Errorf("block note should name the blocked tool, got %q", res.Result.Content)
	}
}

// TestPlanModeBlocksRunCommand：run_command 归为写类，计划模式下同样拦截（R16）。
func TestPlanModeBlocksRunCommand(t *testing.T) {
	run := &fakeTool{name: "run_command", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("c1", "run_command", map[string]any{"command": "go test ./..."})}, stop: provider.StopToolUse},
		{text: "计划", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(run), PlanMode: true, Confirmer: &allowConfirmer{}})

	evs := runLoop(t, a, nil, "跑测试")

	if run.execCount() != 0 {
		t.Error("run_command must be blocked in plan mode")
	}
	if got := countEvents(evs, EventToolBlocked); got != 1 {
		t.Errorf("ToolBlocked = %d, want 1", got)
	}
}

// TestPlanModeOffExecutesWrites：关掉开关后写操作恢复正常（R16）。
func TestPlanModeOffExecutesWrites(t *testing.T) {
	write := &fakeTool{name: "write_file", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("w1", "write_file", map[string]any{"path": "a"})}, stop: provider.StopToolUse},
		{text: "改好了", stop: provider.StopEndTurn},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(write), Confirmer: &allowConfirmer{}})

	evs := runLoop(t, a, nil, "改一下")

	if write.execCount() != 1 {
		t.Errorf("write should run when plan mode is off, ran %d times", write.execCount())
	}
	if got := countEvents(evs, EventToolBlocked); got != 0 {
		t.Errorf("ToolBlocked = %d, want 0", got)
	}
}

// TestPlanBlockedCallsCountTowardRepeatDetection：被拦截的写调用反复出现会触发重复检测，
// 避免模型在计划模式下原地撞墙把轮数烧光。
func TestPlanBlockedCallsCountTowardRepeatDetection(t *testing.T) {
	write := &fakeTool{name: "write_file", confirm: true}
	p := &fakeProvider{turns: []scriptedTurn{
		{calls: []message.ToolCall{call("w1", "write_file", map[string]any{"path": "a"})}, stop: provider.StopToolUse},
	}}
	a := New(Config{Provider: p, Registry: newRegistry(write), PlanMode: true})

	evs := runLoop(t, a, nil, "改一下")

	done := lastEvent(evs)
	if done.StopReason != StopRepeatCall {
		t.Errorf("StopReason = %q, want repeat_call", done.StopReason)
	}
	if p.requests() != 3 {
		t.Errorf("provider called %d times, want 3 (blocked calls count as repeats)", p.requests())
	}
	assertPaired(t, done.Messages)
}

// TestPlanModeKeepsSendingAllToolDefinitions：工具定义不裁剪，全量下发（决策 7）。
func TestPlanModeKeepsSendingAllToolDefinitions(t *testing.T) {
	read := &fakeTool{name: "read_file", readOnly: true}
	write := &fakeTool{name: "write_file", confirm: true}
	rec := &defRecordingProvider{}
	a := New(Config{Provider: rec, Registry: newRegistry(read, write), PlanMode: true})

	runLoop(t, a, nil, "hi")

	if len(rec.defs) != 2 {
		t.Errorf("definitions sent = %d, want all 2 (plan mode must not trim)", len(rec.defs))
	}
}

// defRecordingProvider 记录收到的工具定义，立即结束一轮。
type defRecordingProvider struct {
	defs []tool.Definition
}

func (d *defRecordingProvider) Stream(_ context.Context, _ string, defs []tool.Definition, _ []message.Message) <-chan provider.StreamEvent {
	d.defs = defs
	ch := make(chan provider.StreamEvent, 1)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventDone, StopReason: provider.StopEndTurn}
	}()
	return ch
}
