package context

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/tool"
)

// fakeProvider 捕获收到的 system/msgs 并返回预设文本。
type fakeProvider struct {
	text      string
	err       error
	gotSystem string
	gotMsgs   []message.Message
}

func (f *fakeProvider) Stream(ctx context.Context, system string, tools []tool.Definition, msgs []message.Message) <-chan provider.StreamEvent {
	f.gotSystem = system
	f.gotMsgs = msgs
	ch := make(chan provider.StreamEvent, 2)
	go func() {
		defer close(ch)
		if f.err != nil {
			ch <- provider.StreamEvent{Type: provider.EventError, Err: f.err}
			return
		}
		ch <- provider.StreamEvent{Type: provider.EventText, Text: f.text}
		ch <- provider.StreamEvent{Type: provider.EventDone}
	}()
	return ch
}

// genMsgs 生成 n 条交替 user/assistant 的消息，每条 content 长度为 c。
func genMsgs(n, c int) []message.Message {
	out := make([]message.Message, n)
	for i := 0; i < n; i++ {
		role := message.RoleUser
		if i%2 == 1 {
			role = message.RoleAssistant
		}
		out[i] = message.Message{Role: role, Content: strings.Repeat("x", c), CreatedAt: time.Now()}
	}
	return out
}

func TestEstimate(t *testing.T) {
	got := Estimate("abcd", []message.Message{{Content: "abcd"}, {Thinking: "abcd"}})
	// 3*4 chars / 4 = 3 tokens
	if got != 3 {
		t.Errorf("Estimate = %d, want 3", got)
	}
	if Estimate("", nil) != 0 {
		t.Errorf("Estimate empty != 0")
	}
}

func TestContextWindow(t *testing.T) {
	cases := []struct {
		profile config.Profile
		want    int
	}{
		{config.Profile{ContextWindow: 123}, 123},
		{config.Profile{Model: "claude-sonnet-5"}, 200000},
		{config.Profile{Model: "gpt-4o-mini"}, 128000},
		{config.Profile{Model: "gpt-4.1"}, 1047576},
		{config.Profile{Model: "deepseek-chat"}, 64000},
		{config.Profile{Model: "unknown-model"}, FallbackContextWindow},
	}
	for _, c := range cases {
		if got := ContextWindow(c.profile); got != c.want {
			t.Errorf("ContextWindow(%+v) = %d, want %d", c.profile, got, c.want)
		}
	}
}

func TestSummarizeNoTriggerUnderThreshold(t *testing.T) {
	p := config.Profile{Model: "claude-x", ContextWindow: 100000} // 阈值很高
	msgs := genMsgs(20, 20)
	fp := &fakeProvider{text: "SUMMARY"}
	out, n, err := Summarize(context.Background(), fp, "", p, msgs, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(out) != 20 {
		t.Errorf("no-trigger: n=%d len=%d, want n=0 len=20", n, len(out))
	}
	if fp.gotSystem != "" {
		t.Errorf("should not have called provider under threshold")
	}
}

func TestSummarizeTriggeredOverThreshold(t *testing.T) {
	p := config.Profile{Model: "claude-x", ContextWindow: 100} // 阈值 80 tokens
	msgs := genMsgs(20, 20)                                    // ~100 tokens -> 触发
	fp := &fakeProvider{text: "这是摘要"}
	out, n, err := Summarize(context.Background(), fp, "", p, msgs, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Errorf("compressed count = %d, want 8", n)
	}
	if len(out) != 13 {
		t.Fatalf("len(out) = %d, want 13 (1 summary + 12 recent)", len(out))
	}
	if !out[0].IsSupplement || out[0].SupplementKind != message.SupplementSummary ||
		out[0].Role != message.RoleUser || !strings.Contains(out[0].Content, "这是摘要") {
		t.Errorf("summary message = %+v", out[0])
	}
	if fp.gotSystem != summariseSystemPrompt {
		t.Errorf("summarize used wrong system prompt: %q", fp.gotSystem)
	}
}

func TestSummarizeForceBypassesThreshold(t *testing.T) {
	p := config.Profile{Model: "claude-x", ContextWindow: 100000} // 阈值很高
	msgs := genMsgs(20, 20)
	fp := &fakeProvider{text: "强制摘要"}
	out, n, err := Summarize(context.Background(), fp, "", p, msgs, true) // force
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 || len(out) != 13 {
		t.Errorf("force: n=%d len=%d, want n=8 len=13", n, len(out))
	}
}

func TestSummarizeTooShortNoOp(t *testing.T) {
	p := config.Profile{Model: "claude-x", ContextWindow: 10} // 阈值 8 tokens，容易触发
	msgs := genMsgs(10, 20)                                   // 但消息数 10 < keep 12
	fp := &fakeProvider{text: "摘要"}
	out, n, err := Summarize(context.Background(), fp, "", p, msgs, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(out) != 10 {
		t.Errorf("too-short: n=%d len=%d, want n=0 len=10", n, len(out))
	}
}

func TestSummarizeProviderError(t *testing.T) {
	p := config.Profile{Model: "claude-x", ContextWindow: 100}
	msgs := genMsgs(20, 20)
	fp := &fakeProvider{err: errors.New("boom")}
	_, n, err := Summarize(context.Background(), fp, "", p, msgs, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if n != 0 {
		t.Errorf("n = %d, want 0 on error", n)
	}
}

func TestSummarizeEmptyResultError(t *testing.T) {
	p := config.Profile{Model: "claude-x", ContextWindow: 100}
	msgs := genMsgs(20, 20)
	fp := &fakeProvider{text: "   "} // 空白
	_, _, err := Summarize(context.Background(), fp, "", p, msgs, false)
	if err == nil {
		t.Fatal("expected error for empty summary")
	}
}

func TestSummarizePreservesEnvSupplement(t *testing.T) {
	p := config.Profile{Model: "claude-x", ContextWindow: 100}
	msgs := []message.Message{message.NewEnvSupplement("ENV-MARK")}
	msgs = append(msgs, genMsgs(20, 20)...)

	fp := &fakeProvider{text: "摘要"}
	out, n, err := Summarize(context.Background(), fp, "", p, msgs, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Errorf("compressed = %d, want 8", n)
	}
	if len(out) != 14 {
		t.Fatalf("len(out) = %d, want 14 (env + summary + 12)", len(out))
	}
	if !out[0].IsSupplement || out[0].SupplementKind != message.SupplementEnv || !strings.Contains(out[0].Content, "ENV-MARK") {
		t.Errorf("env 补充消息 not preserved first: %+v", out[0])
	}
	if out[1].SupplementKind != message.SupplementSummary || out[1].SupplementSeq != 2 {
		t.Errorf("summary 补充消息 = %+v", out[1])
	}
}

func TestSummarizeKeepsToolPair(t *testing.T) {
	p := config.Profile{Model: "claude-x", ContextWindow: 100}
	msgs := make([]message.Message, 0, 21)
	for i := 0; i < 8; i++ {
		role := message.RoleUser
		if i%2 == 1 {
			role = message.RoleAssistant
		}
		msgs = append(msgs, message.NewMessage(role, "filler"))
	}
	msgs = append(msgs, message.Message{
		Role:      message.RoleAssistant,
		ToolCalls: []message.ToolCall{{ID: "t1", Name: "read_file"}},
	})
	msgs = append(msgs, message.Message{
		Role:        message.RoleUser,
		ToolResults: []message.ToolResult{{ID: "t1", Content: "ok"}},
	})
	for i := 0; i < 11; i++ {
		role := message.RoleUser
		if i%2 == 1 {
			role = message.RoleAssistant
		}
		msgs = append(msgs, message.NewMessage(role, "after"))
	}

	fp := &fakeProvider{text: "摘要"}
	out, n, err := Summarize(context.Background(), fp, "", p, msgs, true)
	if err != nil {
		t.Fatal(err)
	}
	// 切分点原来落在 tool_result 上，safeCut 向前移到 assistant.tool_use。
	if n != 8 {
		t.Errorf("compressed = %d, want 8", n)
	}
	found := false
	for i, m := range out {
		if len(m.ToolCalls) == 0 {
			continue
		}
		found = true
		if i+1 >= len(out) || len(out[i+1].ToolResults) != 1 || out[i+1].ToolResults[0].ID != "t1" {
			t.Fatalf("tool_use not followed by paired tool_result: %+v", out)
		}
	}
	if !found {
		t.Fatal("tool pair missing from summary output")
	}
}

func TestBuildTranscriptLabelsSupplement(t *testing.T) {
	note := message.NewModeSupplement(2, true, true, "plan body")
	tr := buildTranscript([]message.Message{note, message.NewMessage(message.RoleUser, "question")})
	if !strings.Contains(tr, "[系统补充]\n") {
		t.Errorf("transcript missing 补充消息 marker:\n%s", tr)
	}
	if !strings.Contains(tr, "plan body") {
		t.Errorf("transcript missing note body:\n%s", tr)
	}
	if !strings.Contains(tr, "[用户]\nquestion") {
		t.Errorf("transcript missing user message:\n%s", tr)
	}
}
