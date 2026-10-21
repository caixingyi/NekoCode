package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/agent"
	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/session"
	"nekocode/internal/tool"
)

// jsonQuote 把字符串转成 JSON 字符串字面量（含引号），供拼进 SSE。
func jsonQuote(s string) string {
	out := []rune{'"'}
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		default:
			out = append(out, r)
		}
	}
	return string(append(out, '"'))
}

// anthropicTurns 返回一个按脚本应答的 httptest：每次请求消费一个响应体，用尽后重复最后一个。
func sseServer(t *testing.T, bodies ...string) *httptest.Server {
	t.Helper()
	var n int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&n, 1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(bodies[i]))
	}))
}

// anthropicToolUse 拼一段「请求调用某工具」的 Anthropic SSE（content block 下标为 0）。
func anthropicToolUse(id, name, argsJSON string) string {
	return anthropicToolUseAt(0, id, name, argsJSON)
}

// anthropicToolUseAt 同上，但指定 content block 下标——一次回复里多个工具时下标必须不同，
// provider 按 index 归并 block。只发 block，不结束消息（用 anthropicStop 收尾）。
func anthropicToolUseAt(idx int, id, name, argsJSON string) string {
	i := strconv.Itoa(idx)
	return "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":" + i + ",\"content_block\":{\"type\":\"tool_use\",\"id\":\"" + id + "\",\"name\":\"" + name + "\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":" + i + ",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":" + jsonQuote(argsJSON) + "}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":" + i + "}\n\n"
}

// anthropicText 拼一段「返回文本」的 Anthropic SSE（只发 block）。
func anthropicText(text string) string {
	return "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":" + jsonQuote(text) + "}}\n\n"
}

// anthropicStop 结束一次 Anthropic 消息。
func anthropicStop() string {
	return "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
}

// ---- 单轮 / 多轮 ----

func TestToolFlowAutoReadFile(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello-content"), 0o644)
	srv := sseServer(t,
		anthropicToolUse("tu_1", "read_file", `{"path":"a.txt"}`)+anthropicStop(),
		anthropicText("文件读到了")+anthropicStop(),
	)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, root)
	m.input.SetValue("读 a.txt")
	h := newHarness(t, m)
	cmd := h.m.startRun("读 a.txt")
	m = h.run(cmd)

	// 期望：user, assistant(tool_calls), user(tool_results), assistant(final)
	msgs := m.sess.Messages
	if len(msgs) != 4 {
		t.Fatalf("len(msgs) = %d, want 4: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != message.RoleUser || msgs[0].Content != "读 a.txt" {
		t.Errorf("first message = %+v", msgs[0])
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].Name != "read_file" {
		t.Errorf("assistant tool_calls = %+v", msgs[1].ToolCalls)
	}
	if len(msgs[2].ToolResults) != 1 || msgs[2].ToolResults[0].Content != "hello-content" {
		t.Errorf("tool result = %+v", msgs[2].ToolResults)
	}
	if msgs[3].Role != message.RoleAssistant || msgs[3].Content != "文件读到了" {
		t.Errorf("final = %+v", msgs[3])
	}
	if m.busy() {
		t.Error("should be idle after flow")
	}
	pairedHistory(t, m)
}

// TestLoopContinuesAcrossTurns：连续两轮工具调用后给出最终答复（ReAct 循环，AC1）。
func TestLoopContinuesAcrossTurns(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("AAA"), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("BBB"), 0o644)
	srv := sseServer(t,
		anthropicToolUse("t1", "read_file", `{"path":"a.txt"}`)+anthropicStop(),
		anthropicToolUse("t2", "read_file", `{"path":"b.txt"}`)+anthropicStop(),
		anthropicText("两个都读完了")+anthropicStop(),
	)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, root)
	h := newHarness(t, m)
	m = h.run(h.m.startRun("读两个文件"))

	// user + (assistant, results) × 2 + assistant(final) = 6
	if len(m.sess.Messages) != 6 {
		t.Fatalf("len(msgs) = %d, want 6: %+v", len(m.sess.Messages), m.sess.Messages)
	}
	if m.turn != 3 {
		t.Errorf("turn = %d, want 3", m.turn)
	}
	var texts []string
	for _, msg := range m.sess.Messages {
		if len(msg.ToolResults) > 0 {
			texts = append(texts, msg.ToolResults[0].Content)
		}
	}
	if len(texts) != 2 || texts[0] != "AAA" || texts[1] != "BBB" {
		t.Errorf("tool results = %v", texts)
	}
	pairedHistory(t, m)
}

// ---- 确认 ----

func TestToolFlowConfirmReject(t *testing.T) {
	root := t.TempDir()
	srv := sseServer(t,
		anthropicToolUse("w1", "write_file", `{"path":"new.txt","content":"data"}`)+anthropicStop(),
		anthropicText("好的没写")+anthropicStop(),
	)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, root)
	h := newHarness(t, m).autoAnswer(agent.DecisionDeny)
	m = h.run(h.m.startRun("写文件"))

	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Error("file should not be created on reject")
	}
	var rejected bool
	for _, msg := range m.sess.Messages {
		for _, tr := range msg.ToolResults {
			if tr.IsError && strings.Contains(tr.Content, "拒绝") {
				rejected = true
			}
		}
	}
	if !rejected {
		t.Errorf("expected rejection tool result: %+v", m.sess.Messages)
	}
	pairedHistory(t, m)
}

func TestToolFlowConfirmAllowWrites(t *testing.T) {
	root := t.TempDir()
	srv := sseServer(t,
		anthropicToolUse("w1", "write_file", `{"path":"new.txt","content":"data"}`)+anthropicStop(),
		anthropicText("已写")+anthropicStop(),
	)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, root)
	h := newHarness(t, m).autoAnswer(agent.DecisionAllow)
	m = h.run(h.m.startRun("写文件"))

	data, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil || string(data) != "data" {
		t.Errorf("file not written: %v %q", err, data)
	}
}

// TestAllowAllPersistsAcrossMessages：「本会话全部允许」在后续消息里不再询问（R13）。
func TestAllowAllPersistsAcrossMessages(t *testing.T) {
	root := t.TempDir()
	srv := sseServer(t,
		anthropicToolUse("w1", "write_file", `{"path":"one.txt","content":"1"}`)+anthropicStop(),
		anthropicText("第一次写完")+anthropicStop(),
		anthropicToolUse("w2", "write_file", `{"path":"two.txt","content":"2"}`)+anthropicStop(),
		anthropicText("第二次写完")+anthropicStop(),
	)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, root)
	h := newHarness(t, m).autoAnswer(agent.DecisionAllowAll)
	m = h.run(h.m.startRun("第一次"))
	m = h.run(h.m.startRun("第二次"))

	for _, name := range []string{"one.txt", "two.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s should be written: %v", name, err)
		}
	}
	if !m.allowAll.Load() {
		t.Error("allowAll should stay on for the session")
	}
}

// ---- 多工具 ----

func TestToolFlowMultipleTools(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("AA"), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("BB"), 0o644)
	srv := sseServer(t,
		anthropicToolUseAt(0, "t1", "read_file", `{"path":"a.txt"}`)+
			anthropicToolUseAt(1, "t2", "read_file", `{"path":"b.txt"}`)+
			anthropicStop(),
		anthropicText("两个都读了")+anthropicStop(),
	)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, root)
	h := newHarness(t, m)
	m = h.run(h.m.startRun("读 a 和 b"))

	var callCount, resCount int
	for _, msg := range m.sess.Messages {
		callCount += len(msg.ToolCalls)
		resCount += len(msg.ToolResults)
	}
	if callCount != 2 || resCount != 2 {
		t.Fatalf("callCount=%d resCount=%d, want 2/2: %+v", callCount, resCount, m.sess.Messages)
	}
	pairedHistory(t, m)
}

func TestToolFlowOpenAIEndToEnd(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("openai-read"), 0o644)
	srv := sseServer(t,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read_file","arguments":"{\"path\":\"a.txt\"}"}}]}}]}`+"\n\n"+
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n"+
			`data: [DONE]`+"\n\n",
		`data: {"choices":[{"delta":{"content":"读到了"}}]}`+"\n\n"+`data: [DONE]`+"\n\n",
	)
	defer srv.Close()

	m := openaiModel(t, srv.URL, root)
	h := newHarness(t, m)
	m = h.run(h.m.startRun("读 a.txt"))

	msgs := m.sess.Messages
	if len(msgs) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(msgs), msgs)
	}
	if len(msgs[2].ToolResults) != 1 || msgs[2].ToolResults[0].Content != "openai-read" {
		t.Errorf("tool result = %+v", msgs[2].ToolResults)
	}
	if msgs[3].Content != "读到了" {
		t.Errorf("final = %q", msgs[3].Content)
	}
	pairedHistory(t, m)
}

// ---- plan 模式 ----

func TestPlanModeBlocksWritesEndToEnd(t *testing.T) {
	root := t.TempDir()
	srv := sseServer(t,
		anthropicToolUse("w1", "write_file", `{"path":"new.txt","content":"x"}`)+anthropicStop(),
		anthropicText("这是我的计划：先做 A 再做 B")+anthropicStop(),
	)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, root)
	m.handleCommand("/plan")
	if !m.sess.PlanMode {
		t.Fatal("/plan 未打开计划模式")
	}

	h := newHarness(t, m).autoAnswer(agent.DecisionAllow)
	m = h.run(h.m.startRun("帮我改一下"))

	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Error("plan 模式下写操作必须被拦截")
	}
	var blocked bool
	for _, msg := range m.sess.Messages {
		for _, tr := range msg.ToolResults {
			if strings.Contains(tr.Content, "计划模式") {
				blocked = true
			}
		}
	}
	if !blocked {
		t.Errorf("expected plan-block tool result: %+v", m.sess.Messages)
	}
	if !strings.Contains(m.notice, "计划模式") {
		t.Errorf("notice should mention plan mode, got %q", m.notice)
	}
	pairedHistory(t, m)
}

// ---- 取消 ----

// TestCtrlCDuringRunCancels：循环进行中按 Ctrl+C 请求取消，而不是退出（R18）。
func TestCtrlCDuringRunCancels(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	canceled := false
	m.cancel = func() { canceled = true }

	m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))

	if !canceled {
		t.Error("Ctrl+C 在循环中应取消循环")
	}
	if m.quitting {
		t.Error("Ctrl+C 在循环中不应退出程序")
	}
	if !strings.Contains(m.notice, "取消") {
		t.Errorf("notice = %q", m.notice)
	}
}

// TestEscDuringRunCancels：Esc 同样取消循环。
func TestEscDuringRunCancels(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	canceled := false
	m.cancel = func() { canceled = true }

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))

	if !canceled {
		t.Error("Esc 在循环中应取消循环")
	}
}

// TestEscInConfirmDeniesOnly：确认态下 Esc 只拒绝当前工具，不取消循环（R19）。
func TestEscInConfirmDeniesOnly(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	canceled := false
	m.cancel = func() { canceled = true }
	reply := make(chan agent.Decision, 1)
	m.confirm = &confirmRequestMsg{
		call:  message.ToolCall{ID: "t1", Name: "write_file"},
		reply: reply,
	}

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))

	if canceled {
		t.Error("确认态下 Esc 不应取消整个循环")
	}
	select {
	case d := <-reply:
		if d != agent.DecisionDeny {
			t.Errorf("decision = %v, want Deny", d)
		}
	default:
		t.Error("Esc 应把拒绝决定回给 agent")
	}
	if m.confirm != nil {
		t.Error("确认态应已清空")
	}
}

// TestConfirmKeysMapToDecisions：y/a/n 分别映射到允许 / 全部允许 / 拒绝。
func TestConfirmKeysMapToDecisions(t *testing.T) {
	cases := []struct {
		key  rune
		want agent.Decision
	}{
		{'y', agent.DecisionAllow},
		{'a', agent.DecisionAllowAll},
		{'n', agent.DecisionDeny},
	}
	for _, c := range cases {
		m := newTestModel(t)
		m.running = true
		reply := make(chan agent.Decision, 1)
		m.confirm = &confirmRequestMsg{call: message.ToolCall{ID: "t1"}, reply: reply}

		m.Update(tea.KeyPressMsg(tea.Key{Code: c.key, Text: string(c.key)}))

		if d := <-reply; d != c.want {
			t.Errorf("%q → %v, want %v", c.key, d, c.want)
		}
		if c.want == agent.DecisionAllowAll && !m.allowAll.Load() {
			t.Error("按 a 之后 allowAll 应为真")
		}
	}
}

// TestStopReasonNotices：各类收场原因都要给出可读提示（AC6）。
func TestStopReasonNotices(t *testing.T) {
	cases := map[agent.StopReason]string{
		agent.StopMaxTurns:      "上限",
		agent.StopMaxTokens:     "截断",
		agent.StopRepeatCall:    "重复",
		agent.StopContextLimit:  "/compact",
		agent.StopCanceled:      "取消",
		agent.StopEmptyResponse: "没有返回",
	}
	for reason, want := range cases {
		if got := stopReasonNotice(reason, 0); !strings.Contains(got, want) {
			t.Errorf("stopReasonNotice(%q) = %q, want contains %q", reason, got, want)
		}
	}
	if got := stopReasonNotice(agent.StopEndTurn, 0); got != "" {
		t.Errorf("正常结束不该有提示，got %q", got)
	}
	if got := stopReasonNotice(agent.StopEndTurn, 2); !strings.Contains(got, "计划模式") {
		t.Errorf("被 plan 拦截过应有提示，got %q", got)
	}
}

// ---- 持久化 ----

// TestRunPersistsHistory：每轮快照都落盘，且落盘历史始终配对（R22）。
func TestRunPersistsHistory(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644)
	srv := sseServer(t,
		anthropicToolUse("t1", "read_file", `{"path":"a.txt"}`)+anthropicStop(),
		anthropicText("done")+anthropicStop(),
	)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, root)
	h := newHarness(t, m)
	m = h.run(h.m.startRun("读"))

	list, err := m.store.List(0)
	if err != nil || len(list) != 1 {
		t.Fatalf("store.List = %v, %v", list, err)
	}
	if len(list[0].Messages) != 4 {
		t.Errorf("persisted messages = %d, want 4", len(list[0].Messages))
	}
}

// TestPlanModePersistsAcrossResume：/plan 随会话落盘，/resume 恢复（R14）。
func TestPlanModePersistsAcrossResume(t *testing.T) {
	m := newTestModel(t)
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "旧话题")}
	m.handleCommand("/plan")
	if !m.sess.PlanMode {
		t.Fatal("plan 未开启")
	}
	m.saveSession()

	// 新开会话后恢复，开关应一并回来。
	m.handleCommand("/new")
	if m.sess.PlanMode {
		t.Fatal("/new 应重置为普通模式")
	}
	m.handleCommand("/resume 1")
	if !m.sess.PlanMode {
		t.Error("/resume 应恢复计划模式开关")
	}
}

// TestPlanCommandToggles：/plan 反复切换并给出提示。
func TestPlanCommandToggles(t *testing.T) {
	m := newTestModel(t)
	m.handleCommand("/plan")
	if !m.sess.PlanMode || !strings.Contains(m.notice, "计划模式") {
		t.Errorf("第一次 /plan 应开启，notice=%q", m.notice)
	}
	m.handleCommand("/plan")
	if m.sess.PlanMode || !strings.Contains(m.notice, "退出") {
		t.Errorf("第二次 /plan 应关闭，notice=%q", m.notice)
	}
}

// TestHeaderShowsPlanBadge：状态栏体现计划模式。
func TestHeaderShowsPlanBadge(t *testing.T) {
	m := newTestModel(t)
	if strings.Contains(m.header(), "[PLAN]") {
		t.Error("普通模式下不该显示 PLAN 标识")
	}
	m.sess.PlanMode = true
	if !strings.Contains(m.header(), "[PLAN]") {
		t.Error("计划模式下应显示 PLAN 标识")
	}
}

// 确保 registry 装配路径没被改坏。
func TestModelWithCustomRegistry(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"p": {Protocol: config.ProtocolOpenAI, Model: "m", APIKey: "k"},
	}}
	reg := tool.NewRegistry(t.TempDir())
	m := modelWith(t, cfg, "p", reg)
	if m.registry != reg {
		t.Error("registry 未注入")
	}
	_ = session.NewSession("p")
}

// ---- token 用量 ----

// TestTokenUsageParsedAndShown：两家协议的用量都被解析出来并显示在状态栏（AC8、R25）。
func TestTokenUsageParsedAndShown(t *testing.T) {
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":0}}}\n\n" +
		anthropicText("hi") +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n" +
		anthropicStop()
	srv := sseServer(t, sse)
	defer srv.Close()

	m := anthropicModel(t, srv.URL, t.TempDir())
	h := newHarness(t, m)
	m = h.run(h.m.startRun("hi"))

	if m.totalUsage.InputTokens != 11 || m.totalUsage.OutputTokens != 7 {
		t.Errorf("usage = %+v, want {11 7}", m.totalUsage)
	}
	header := m.header()
	if !strings.Contains(header, "↑11") || !strings.Contains(header, "↓7") {
		t.Errorf("状态栏应显示 token 用量: %q", header)
	}
}

// TestOpenAIUsageParsed：OpenAI 侧同样解析 usage（需要 stream_options，服务端回传 usage chunk）。
func TestOpenAIUsageParsed(t *testing.T) {
	srv := sseServer(t,
		`data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n"+
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
			`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4}}`+"\n\n"+
			`data: [DONE]`+"\n\n",
	)
	defer srv.Close()

	m := openaiModel(t, srv.URL, t.TempDir())
	h := newHarness(t, m)
	m = h.run(h.m.startRun("hi"))

	if m.totalUsage.InputTokens != 3 || m.totalUsage.OutputTokens != 4 {
		t.Errorf("usage = %+v, want {3 4}", m.totalUsage)
	}
}

// TestHeaderShowsTurnAndToolDuration：循环中的轮次与最近一次工具耗时可见（R3）。
func TestHeaderShowsTurnAndToolDuration(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.turn = 2
	m.lastTool = "read_file"
	m.lastExec = 12 * time.Millisecond

	h := m.header()
	if !strings.Contains(h, "第 2 轮") {
		t.Errorf("状态栏应显示轮次: %q", h)
	}
	if !strings.Contains(h, "read_file") || !strings.Contains(h, "12ms") {
		t.Errorf("状态栏应显示工具耗时: %q", h)
	}
}
