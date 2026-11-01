package agent

import (
	"strings"
	"time"

	"nekocode/internal/environment"
	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/tool"
)

// 为「没有真正执行的工具调用」合成结果时使用的文案。
const (
	noteCanceled      = "用户取消，该工具未执行"
	noteUpstreamError = "上游请求出错，该工具未执行"
	noteTruncated     = "模型输出被长度截断，该工具未执行"
	noteRepeatCall    = "检测到重复的工具调用，循环已中止，该工具未执行"
)

// turnResult 是一轮模型调用的产出。
type turnResult struct {
	text       string
	thinking   string
	signature  string
	calls      []message.ToolCall
	stopReason string
	usage      provider.Usage
	err        error
}

// assistant 把本轮产出组装成 assistant 消息。
func (t turnResult) assistant() message.Message {
	return message.Message{
		Role:      message.RoleAssistant,
		Content:   t.text,
		Thinking:  t.thinking,
		Signature: t.signature,
		ToolCalls: t.calls,
		CreatedAt: time.Now(),
	}
}

// isEmpty 表示模型既没输出文本也没请求工具（允许只有 thinking）（R5）。
func (t turnResult) isEmpty() bool {
	return strings.TrimSpace(t.text) == "" && len(t.calls) == 0
}

// loop 是 ReAct 循环主体（R1）。每轮以「事件 + 历史快照」对外汇报，
// 退出前一定发出 EventDone。
func (r *run) loop(history []message.Message, input string) {
	msgs := r.prepareHistory(history, input)
	r.send(Event{Type: EventUserMessage, Text: input, Messages: cloneMessages(msgs)})

	var total provider.Usage
	for turn := 1; ; turn++ {
		// 取消后不再发起新的模型调用（R18）。上一轮的历史已在提交时补齐，可直接收尾。
		if r.ctx.Err() != nil {
			r.send(Event{
				Type: EventDone, Turn: turn - 1, StopReason: StopCanceled,
				Usage: total, Messages: cloneMessages(msgs),
			})
			return
		}
		// R6：轮数上限在每轮开始前判定，因此永远不会留下没有结果的 tool_use。
		if turn > r.agent.cfg.MaxTurns {
			r.send(Event{
				Type: EventDone, Turn: turn - 1, StopReason: StopMaxTurns,
				Usage: total, Messages: cloneMessages(msgs),
			})
			return
		}

		msgs = r.injectModeSupplement(msgs, turn)
		res := r.runTurn(msgs, turn)
		total = total.Add(res.usage)

		asst := res.assistant()
		turnStop := StopReason(res.stopReason)

		emitTurnEnd := func() {
			r.send(Event{
				Type: EventTurnEnd, Turn: turn, StopReason: turnStop,
				Usage: res.usage, Messages: cloneMessages(msgs),
			})
		}
		// finish 收尾：先汇报本轮，再汇报整个循环的结束原因与累计用量。
		finish := func(reason StopReason) {
			emitTurnEnd()
			r.send(Event{
				Type: EventDone, Turn: turn, StopReason: reason,
				Usage: total, Messages: cloneMessages(msgs),
			})
		}

		// 以下四条终止路径都必须先为已有的 tool_use 补上配对结果，
		// 否则历史会残缺，下一次请求会被 Anthropic 直接拒绝（R9、R22）。
		//
		// 取消先于错误判定：取消会让正在进行的 HTTP 请求以「context canceled」失败，
		// 那不该报成上游错误。
		if r.ctx.Err() != nil {
			msgs = commitTurn(msgs, asst, synthesizeResults(res.calls, noteCanceled))
			finish(StopCanceled)
			return
		}
		if res.err != nil {
			msgs = commitTurn(msgs, asst, synthesizeResults(res.calls, noteUpstreamError))
			r.send(Event{Type: EventError, Turn: turn, Err: res.err, Messages: cloneMessages(msgs)})
			finish(StopError)
			return
		}
		if turnStop == StopMaxTokens {
			msgs = commitTurn(msgs, asst, synthesizeResults(res.calls, noteTruncated))
			finish(StopMaxTokens)
			return
		}
		if len(res.calls) == 0 {
			msgs = commitTurn(msgs, asst, nil)
			if res.isEmpty() {
				finish(StopEmptyResponse)
			} else {
				finish(StopEndTurn)
			}
			return
		}
		// R8：重复调用在执行前判定，避免白跑工具。
		if r.detector.feed(res.calls) {
			msgs = commitTurn(msgs, asst, synthesizeResults(res.calls, noteRepeatCall))
			finish(StopRepeatCall)
			return
		}

		// 有工具调用且判定继续：执行、回填，然后进入下一轮。
		results := r.runBatch(res.calls, turn)
		msgs = commitTurn(msgs, asst, results)
		emitTurnEnd()

		// R21：循环内不做压缩，超限即中止并交给用户处理。
		if r.agent.cfg.ContextLimit != nil && r.agent.cfg.ContextLimit(msgs) {
			r.send(Event{
				Type: EventDone, Turn: turn, StopReason: StopContextLimit,
				Usage: total, Messages: cloneMessages(msgs),
			})
			return
		}
	}
}

// prepareHistory 复制历史，确保 env 补充消息在首位，再追加本次用户输入。
func (r *run) prepareHistory(history []message.Message, input string) []message.Message {
	msgs := cloneMessages(history)
	msgs = r.ensureEnvSupplement(msgs)
	return append(msgs, message.NewMessage(message.RoleUser, input))
}

// ensureEnvSupplement 保证环境补充消息存在且位于首位。
// RefreshEnv 为真时重新采集并替换；否则保留快照，只做位置/去重整理。
func (r *run) ensureEnvSupplement(msgs []message.Message) []message.Message {
	idx := message.FindLastSupplement(msgs, message.SupplementEnv)
	if idx >= 0 && !r.agent.cfg.RefreshEnv {
		supp := msgs[idx]
		out := make([]message.Message, 0, len(msgs))
		out = append(out, supp)
		for i, m := range msgs {
			if i == idx || (m.IsSupplement && m.SupplementKind == message.SupplementEnv) {
				continue
			}
			out = append(out, m)
		}
		return out
	}

	collect := r.agent.cfg.CollectEnv
	if collect == nil {
		collect = environment.Collect
	}
	supp := message.NewEnvSupplement(r.agent.cfg.Prompt.EnvSupplement(collect(r.ctx, r.agent.cfg.WorkDir)))
	out := make([]message.Message, 0, len(msgs)+1)
	out = append(out, supp)
	for _, m := range msgs {
		if m.IsSupplement && m.SupplementKind == message.SupplementEnv {
			continue
		}
		out = append(out, m)
	}
	return out
}

// injectModeSupplement 在每轮模型调用前注入 plan 模式提示。
// 第 1、6、11… 轮完整，其余精简；刚由 TUI 追加的完整进入提示可覆盖第 1 轮。
func (r *run) injectModeSupplement(msgs []message.Message, turn int) []message.Message {
	if !r.agent.cfg.PlanMode {
		return msgs
	}
	if turn == 1 && message.HasFreshFullModeSupplement(msgs, true) {
		return msgs
	}
	full := turn == 1 || (turn > 1 && (turn-1)%5 == 0)
	supp := message.NewModeSupplement(message.NextSupplementSeq(msgs), true, full, r.agent.cfg.Prompt.PlanSupplement(full))
	return append(msgs, supp)
}

// runTurn 发起一次模型调用并收集产出。工具调用只在此处累积，
// 对应的 EventToolCallStart 由执行阶段发出（那时才知道批次与是否需要确认）。
func (r *run) runTurn(msgs []message.Message, turn int) turnResult {
	var defs []tool.Definition
	if r.agent.cfg.Registry != nil {
		defs = r.agent.cfg.Registry.Definitions()
	}
	// plan 模式同样下发全部工具定义，只在执行阶段拦截（R15）。
	ch := r.agent.cfg.Provider.Stream(r.ctx, r.agent.cfg.Prompt.System(), defs, msgs)

	var res turnResult
	for ev := range ch {
		switch ev.Type {
		case provider.EventText:
			res.text += ev.Text
			r.send(Event{Type: EventTextDelta, Turn: turn, Text: ev.Text})
		case provider.EventThinking:
			res.thinking += ev.Text
			if ev.Signature != "" {
				res.signature = ev.Signature
			}
			r.send(Event{Type: EventThinkingDelta, Turn: turn, Text: ev.Text, Signature: ev.Signature})
		case provider.EventToolCall:
			res.calls = append(res.calls, ev.ToolCall)
		case provider.EventDone:
			res.stopReason = ev.StopReason
			if ev.Usage != nil {
				res.usage = *ev.Usage
			}
		case provider.EventError:
			if res.err == nil {
				res.err = ev.Err
			}
		}
	}
	return res
}
