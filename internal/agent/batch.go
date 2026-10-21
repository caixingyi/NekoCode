package agent

import (
	"sync"
	"time"

	"nekocode/internal/message"
	"nekocode/internal/tool"
)

// 轮内批次号：读批固定 0、写批固定 1（R3）。事件据此让界面把同批工具渲染在一起。
const (
	readBatchNo  = 0
	writeBatchNo = 1
)

// 工具被拒绝/被拦截时的结构化结果文案。
const (
	noteDenied      = "用户拒绝执行该工具"
	noteNoConfirmer = "没有可用的确认通道，写操作已按安全默认拒绝"
	noteSkipped     = "本批已有写操作被拒绝，该工具一并跳过"
)

// execOutcome 是单次工具执行的结果类别，供批次调度决策。
type execOutcome int

const (
	outcomeOK execOutcome = iota
	outcomeDenied
	outcomeCanceled
	// outcomeBlocked 表示被 plan 模式拦截；它不触发「跳过本批剩余写类」，
	// 因为每一个写类都该各自收到一次明确的拦截说明（R15）。
	outcomeBlocked
)

// runBatch 执行一轮里的全部工具调用，返回与 calls 等长、顺序一致的结果切片。
//
// 调度分两段（R10、R11）：
//  1. 全部读类并发执行，并发上限由 Config.MaxParallel 控制；
//  2. 屏障之后，按模型给出的原始顺序串行执行写类，每个都要确认。
//
// 结果按下标写回数组（并发时每个 goroutine 只写自己的位置），所以回填顺序
// 恒等于模型给出的顺序，无需排序。
func (r *run) runBatch(calls []message.ToolCall, turn int) []message.ToolResult {
	results := make([]message.ToolResult, len(calls))
	reads, writes := r.classify(calls)

	r.runReadBatch(calls, reads, results, turn)

	denied := false
	for _, i := range writes {
		// R13：用户拒绝了某个写类工具后，本批剩余写类全部跳过，
		// 但仍要为它们补结果，保证每个 tool_use 都有配对（历史合法性）。
		if denied {
			results[i] = message.ToolResult{ID: calls[i].ID, Content: noteSkipped, IsError: true}
			r.send(Event{Type: EventToolResult, Turn: turn, Batch: writeBatchNo, Result: results[i]})
			continue
		}
		if r.ctx.Err() != nil {
			results[i] = message.ToolResult{ID: calls[i].ID, Content: noteCanceled, IsError: true}
			r.send(Event{Type: EventToolResult, Turn: turn, Batch: writeBatchNo, Result: results[i]})
			continue
		}
		var outcome execOutcome
		results[i], outcome = r.execOne(calls[i], turn, writeBatchNo)
		if outcome == outcomeDenied {
			denied = true
		}
	}
	return results
}

// runReadBatch 并发执行读类工具。每个 goroutine 只写 results 里属于自己的下标，
// 因此无需加锁也不会串位；wg.Wait 提供写与后续读取之间的 happens-before。
// sem 控制并发上限，避免同时跑太多工具导致资源耗尽。
func (r *run) runReadBatch(calls []message.ToolCall, idx []int, results []message.ToolResult, turn int) {
	if len(idx) == 0 {
		return
	}
	sem := make(chan struct{}, r.agent.cfg.MaxParallel)
	var wg sync.WaitGroup

	for _, i := range idx {
		// 已经取消时不再启动新工具，未执行的补「已取消」结果（R9）。
		if r.ctx.Err() != nil {
			results[i] = message.ToolResult{ID: calls[i].ID, Content: noteCanceled, IsError: true}
			r.send(Event{Type: EventToolResult, Turn: turn, Batch: readBatchNo, Result: results[i]})
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i], _ = r.execOne(calls[i], turn, readBatchNo)
		}(i)
	}
	wg.Wait()
}

// classify 把调用分成「可并发」与「需串行」两组，返回各自在 calls 里的原始下标。
// 只有既只读、又无需确认的工具才并发；未知工具与写类一律走串行，按最坏情况处理。
func (r *run) classify(calls []message.ToolCall) (concurrent, serial []int) {
	for i, c := range calls {
		t, found := r.lookup(c.Name)
		if found && t.IsReadOnly() && !t.NeedsConfirm() {
			concurrent = append(concurrent, i)
			continue
		}
		serial = append(serial, i)
	}
	return concurrent, serial
}

// execOne 执行单个工具：确认（写类）→ 执行 → 发出开始/结果事件。
func (r *run) execOne(call message.ToolCall, turn, batch int) (message.ToolResult, execOutcome) {
	t, found := r.lookup(call.Name)
	if !found {
		result := message.ToolResult{ID: call.ID, Content: "未知工具: " + call.Name, IsError: true}
		r.send(Event{Type: EventToolResult, Turn: turn, Batch: batch, Result: result})
		return result, outcomeOK
	}

	// 工具执行前的统一拦截位（R27），当前唯一规则是 plan-only（R15、R16）。
	// 放在确认之前：计划模式下写操作连问都不问，用户要放行必须先退出计划模式。
	if r.agent.cfg.PlanMode && !t.IsReadOnly() {
		result := planBlockedResult(call)
		r.send(Event{Type: EventToolBlocked, Turn: turn, Batch: batch, ToolCall: call})
		r.send(Event{Type: EventToolResult, Turn: turn, Batch: batch, Result: result})
		return result, outcomeBlocked
	}

	if t.NeedsConfirm() && !r.allowAll {
		// 无确认通道时按拒绝处理（安全默认），不能让写操作静默通过。
		if r.agent.cfg.Confirmer == nil {
			result := message.ToolResult{ID: call.ID, Content: noteNoConfirmer, IsError: true}
			r.send(Event{Type: EventToolResult, Turn: turn, Batch: batch, Result: result})
			return result, outcomeDenied
		}
		r.send(Event{
			Type: EventToolCallStart, Turn: turn, Batch: batch,
			ToolCall: call, NeedsConfirm: true,
		})
		decision, err := r.agent.cfg.Confirmer.Confirm(r.ctx, call, Describe(call))
		if err != nil {
			result := message.ToolResult{ID: call.ID, Content: noteCanceled, IsError: true}
			r.send(Event{Type: EventToolResult, Turn: turn, Batch: batch, Result: result})
			return result, outcomeCanceled
		}
		switch decision {
		case DecisionAllowAll:
			r.allowAll = true
		case DecisionAllow:
		default:
			result := message.ToolResult{ID: call.ID, Content: noteDenied, IsError: true}
			r.send(Event{Type: EventToolRejected, Turn: turn, Batch: batch, ToolCall: call})
			r.send(Event{Type: EventToolResult, Turn: turn, Batch: batch, Result: result})
			return result, outcomeDenied
		}
	} else {
		r.send(Event{Type: EventToolCallStart, Turn: turn, Batch: batch, ToolCall: call})
	}

	start := time.Now()
	res := r.agent.cfg.Registry.Execute(r.ctx, call.Name, call.Input)
	duration := time.Since(start)

	content := res.Content
	if content == "" {
		content = "(无输出)"
	}
	result := message.ToolResult{ID: call.ID, Content: content, IsError: !res.OK}
	r.send(Event{
		Type: EventToolResult, Turn: turn, Batch: batch,
		Result: result, Duration: duration,
	})
	return result, outcomeOK
}

// lookup 按名取工具；registry 未配置时视作不存在。
func (r *run) lookup(name string) (tool.Tool, bool) {
	if r.agent.cfg.Registry == nil {
		return nil, false
	}
	return r.agent.cfg.Registry.Get(name)
}
