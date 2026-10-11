package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/session"
	"nekocode/internal/tool"
)

// drive 模拟 bubbletea 循环：跑 cmd → 得 msg → Update，直到 cmd 为空。
// 遇到工具确认（m.confirming）时按 auto 自动回应。maxSteps 防死循环。
func drive(t *testing.T, m *Model, cmd tea.Cmd, auto rune) *Model {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cmd == nil {
			if m.confirming != nil {
				mm, c := m.confirmResponse(auto)
				m, cmd = mm.(*Model), c
				continue
			}
			return m
		}
		msg := cmd()
		mm, c := m.Update(msg)
		m, cmd = mm.(*Model), c
	}
	t.Fatal("drive: exceeded max steps")
	return m
}

func toolTestModel(t *testing.T, srvURL, root string) *Model {
	t.Helper()
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"claude": {Protocol: config.ProtocolAnthropic, Model: "m", APIKey: "k", BaseURL: srvURL},
	}}
	reg := tool.NewRegistry(root)
	tool.RegisterAll(reg, root)
	m, err := New(cfg, "claude", session.NewStore(t.TempDir()), reg)
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	return m
}

// anthropicToolThenText 返回一个 httptest：首次请求回 tool_use，其后回最终文本。
func anthropicToolThenText(t *testing.T, toolName, argsJSON, finalText string) *httptest.Server {
	var n int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if atomic.AddInt32(&n, 1) == 1 {
			w.Write([]byte(
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu_1\",\"name\":\"" + toolName + "\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":" + jsonQuote(argsJSON) + "}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			return
		}
		w.Write([]byte(
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":" + jsonQuote(finalText) + "}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
}

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

func TestToolFlowAutoReadFile(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello-content"), 0o644)
	srv := anthropicToolThenText(t, "read_file", `{"path":"a.txt"}`, "文件读到了")
	defer srv.Close()

	m := toolTestModel(t, srv.URL, root)
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "读 a.txt")}
	m = drive(t, m, m.startStream(), 'y')

	// 期望消息序列：user, assistant(tool_calls), user(tool_results), assistant(final)
	msgs := m.sess.Messages
	if len(msgs) != 4 {
		t.Fatalf("len(msgs) = %d, want 4: %+v", len(msgs), msgs)
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
}

func TestToolFlowConfirmReject(t *testing.T) {
	root := t.TempDir()
	srv := anthropicToolThenText(t, "write_file", `{"path":"new.txt","content":"data"}`, "好的没写")
	defer srv.Close()

	m := toolTestModel(t, srv.URL, root)
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "写文件")}
	m = drive(t, m, m.startStream(), 'n') // 拒绝

	// 文件不应被创建
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Error("file should not be created on reject")
	}
	// tool result 应为拒绝
	var rejected bool
	for _, msg := range m.sess.Messages {
		for _, tr := range msg.ToolResults {
			if tr.IsError && contains(tr.Content, "拒绝") {
				rejected = true
			}
		}
	}
	if !rejected {
		t.Errorf("expected rejection tool result: %+v", m.sess.Messages)
	}
}

func TestToolFlowConfirmAllowWrites(t *testing.T) {
	root := t.TempDir()
	srv := anthropicToolThenText(t, "write_file", `{"path":"new.txt","content":"data"}`, "已写")
	defer srv.Close()

	m := toolTestModel(t, srv.URL, root)
	m.sess.Messages = []message.Message{message.NewMessage(message.RoleUser, "写文件")}
	m = drive(t, m, m.startStream(), 'a') // 允许

	data, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil || string(data) != "data" {
		t.Errorf("file not written: %v %q", err, data)
	}
	if !m.allowAll {
		t.Error("allowAll should be set after 'a'")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
