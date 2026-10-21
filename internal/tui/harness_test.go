package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/agent"
	"nekocode/internal/config"
	"nekocode/internal/session"
	"nekocode/internal/tool"
)

// harness 模拟 bubbletea 的事件循环：tea.Cmd 在各自的 goroutine 里执行，结果回到
// 唯一的泵 goroutine 里调用 Update——与真实 Program 的单线程模型一致。
//
// 之所以需要它：agent 的确认请求是绕过事件 channel 直接投递的，若不区分两类消息，
// 测试就没法在「agent 正阻塞等待用户按键」时继续推进。
type harness struct {
	t           *testing.T
	m           *Model
	cmdMsgs     chan tea.Msg // tea.Cmd 的执行结果
	uiMsgs      chan tea.Msg // 确认器投递的 UI 消息
	answer      bool         // 是否替用户应答确认请求
	auto        agent.Decision
	outstanding int
}

// chanSender 把确认请求送进测试的消息通道（生产环境里是 *tea.Program.Send）。
type chanSender struct{ ch chan<- tea.Msg }

func (s chanSender) Send(msg tea.Msg) { s.ch <- msg }

// newHarness 构造驱动器，默认不代替用户应答（确认请求会停在界面上）。
func newHarness(t *testing.T, m *Model) *harness {
	t.Helper()
	h := &harness{
		t:       t,
		m:       m,
		cmdMsgs: make(chan tea.Msg, 64),
		uiMsgs:  make(chan tea.Msg, 64),
	}
	m.sender = chanSender{ch: h.uiMsgs}
	return h
}

// autoAnswer 让驱动器在收到确认请求时直接替用户按键。
// 注意不能用 Decision 的零值表示「不应答」——DecisionDeny 本身就是零值。
func (h *harness) autoAnswer(d agent.Decision) *harness {
	h.answer = true
	h.auto = d
	return h
}

func (h *harness) spawn(cmd tea.Cmd) {
	h.outstanding++
	go func() { h.cmdMsgs <- cmd() }()
}

func (h *harness) step(msg tea.Msg) {
	mm, next := h.m.Update(msg)
	h.m = mm.(*Model)
	// 需要时替用户按键应答确认请求，模拟 y/n/a。
	if h.answer && h.m.confirm != nil {
		next = h.m.answerConfirm(h.auto)
	}
	if next != nil {
		h.spawn(next)
	}
}

// run 驱动到所有工作结束（没有在途的 cmd）。
func (h *harness) run(cmd tea.Cmd) *Model {
	h.t.Helper()
	if cmd != nil {
		h.spawn(cmd)
	}
	for steps := 0; h.outstanding > 0; steps++ {
		if steps > 2000 {
			h.t.Fatal("harness: too many steps")
		}
		select {
		case res := <-h.cmdMsgs:
			h.outstanding--
			h.step(res)
		case msg := <-h.uiMsgs:
			h.step(msg)
		case <-time.After(5 * time.Second):
			h.t.Fatalf("harness: stuck; running=%v confirm=%v waiting=%v turn=%d msgs=%d",
				h.m.running, h.m.confirm != nil, h.m.waiting, h.m.turn, len(h.m.sess.Messages))
		}
	}
	return h.m
}

// ---- 测试模型构造 ----

func anthropicModel(t *testing.T, srvURL, root string) *Model {
	t.Helper()
	return testModel(t, config.Profile{
		Protocol: config.ProtocolAnthropic, Model: "m", APIKey: "k", BaseURL: srvURL,
	}, "claude", root)
}

func openaiModel(t *testing.T, srvURL, root string) *Model {
	t.Helper()
	return testModel(t, config.Profile{
		Protocol: config.ProtocolOpenAI, Model: "m", APIKey: "k", BaseURL: srvURL,
	}, "openai", root)
}

// testModel 构造带六个真实工具的 TUI 模型。
func testModel(t *testing.T, p config.Profile, name, root string) *Model {
	t.Helper()
	cfg := &config.Config{Profiles: map[string]config.Profile{name: p}}
	reg := tool.NewRegistry(root)
	tool.RegisterAll(reg, root)
	return modelWith(t, cfg, name, reg)
}

// modelWith 允许测试注入自定义 registry（例如放一个会取消循环的工具）。
func modelWith(t *testing.T, cfg *config.Config, name string, reg *tool.Registry) *Model {
	t.Helper()
	m, err := New(cfg, name, session.NewStore(t.TempDir()), reg)
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	return m
}

// pairedHistory 断言历史里 tool_use 与 tool_result 一一配对。
func pairedHistory(t *testing.T, m *Model) {
	t.Helper()
	pending := map[string]bool{}
	for _, msg := range m.sess.Messages {
		for _, c := range msg.ToolCalls {
			pending[c.ID] = true
		}
		for _, r := range msg.ToolResults {
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
