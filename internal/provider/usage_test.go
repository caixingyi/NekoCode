package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// findDone 返回事件流中第一条 EventDone。
func findDone(t *testing.T, evs []StreamEvent) StreamEvent {
	t.Helper()
	for _, ev := range evs {
		if ev.Type == EventDone {
			return ev
		}
	}
	t.Fatal("no EventDone in stream")
	return StreamEvent{}
}

// TestAnthropicStreamStopReasonAndUsage：stop_reason 归一化 + input/output token 归集（R24、R25）。
func TestAnthropicStreamStopReasonAndUsage(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":34}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newAnthropic(anthropicProfile(srv.URL))
	done := findDone(t, drain(p.Stream(context.Background(), "sys", nil, nil)))

	if done.StopReason != StopMaxTokens {
		t.Errorf("StopReason = %q, want %q", done.StopReason, StopMaxTokens)
	}
	if done.Usage == nil {
		t.Fatal("Usage should not be nil when message_start was received")
	}
	if done.Usage.InputTokens != 12 || done.Usage.OutputTokens != 34 {
		t.Errorf("Usage = %+v, want {12 34}", *done.Usage)
	}
}

// TestAnthropicStreamNoUsage：服务端没回 usage 时保持 nil，不报错。
func TestAnthropicStreamNoUsage(t *testing.T) {
	sse := strings.Join([]string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newAnthropic(anthropicProfile(srv.URL))
	done := findDone(t, drain(p.Stream(context.Background(), "sys", nil, nil)))
	if done.Usage != nil {
		t.Errorf("Usage = %+v, want nil", *done.Usage)
	}
	if done.StopReason != StopEndTurn {
		t.Errorf("StopReason = %q, want %q", done.StopReason, StopEndTurn)
	}
}

// TestOpenAIStreamUsageAndFinishReason：finish_reason=length → max_tokens；usage chunk 被解析（R24、R25）。
func TestOpenAIStreamUsageAndFinishReason(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`, "",
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":7}}`, "",
		`data: [DONE]`, "",
	}, "\n")

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newOpenAI(openaiProfile(srv.URL))
	done := findDone(t, drain(p.Stream(context.Background(), "sys", nil, nil)))

	if !strings.Contains(gotBody, `"stream_options":{"include_usage":true}`) {
		t.Errorf("body should request usage: %s", gotBody)
	}
	if done.StopReason != StopMaxTokens {
		t.Errorf("StopReason = %q, want %q", done.StopReason, StopMaxTokens)
	}
	if done.Usage == nil || done.Usage.InputTokens != 5 || done.Usage.OutputTokens != 7 {
		t.Errorf("Usage = %+v, want {5 7}", done.Usage)
	}
}

// TestOpenAIStreamUsageDisabled：profile 关掉 stream_usage 后请求体不带该字段（兼容不支持它的网关）。
func TestOpenAIStreamUsageDisabled(t *testing.T) {
	sse := strings.Join([]string{`data: [DONE]`, ""}, "\n")
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	profile := openaiProfile(srv.URL)
	off := false
	profile.StreamUsage = &off

	p, _ := newOpenAI(profile)
	done := findDone(t, drain(p.Stream(context.Background(), "sys", nil, nil)))

	if strings.Contains(gotBody, "stream_options") {
		t.Errorf("body should not contain stream_options: %s", gotBody)
	}
	if done.Usage != nil {
		t.Errorf("Usage = %+v, want nil", done.Usage)
	}
}

// TestOpenAIStreamUnknownFinishReason：未知 finish_reason 当正常结束处理，不当截断。
func TestOpenAIStreamUnknownFinishReason(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"content_filter"}]}`, "",
		`data: [DONE]`, "",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newOpenAI(openaiProfile(srv.URL))
	done := findDone(t, drain(p.Stream(context.Background(), "sys", nil, nil)))
	if done.StopReason != StopEndTurn {
		t.Errorf("StopReason = %q, want %q", done.StopReason, StopEndTurn)
	}
}

func TestAnthropicStreamCacheUsage(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":20,\"cache_read_input_tokens\":30,\"output_tokens\":1}}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newAnthropic(anthropicProfile(srv.URL))
	done := findDone(t, drain(p.Stream(context.Background(), "sys", nil, nil)))
	if done.Usage == nil {
		t.Fatal("Usage should not be nil")
	}
	u := *done.Usage
	if u.InputTokens != 60 || u.OutputTokens != 7 {
		t.Errorf("usage = %+v, want total input 60 output 7", u)
	}
	if u.CacheReadTokens != 30 || u.CacheCreationTokens != 20 {
		t.Errorf("cache = read %d create %d, want 30/20", u.CacheReadTokens, u.CacheCreationTokens)
	}
	if !u.CacheUsageKnown {
		t.Error("CacheUsageKnown = false, want true")
	}
	if rate, ok := u.CacheHitRate(); !ok || rate != 0.5 {
		t.Errorf("CacheHitRate = %v, %v; want 0.5, true", rate, ok)
	}
}

func TestOpenAIStreamCacheUsage(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":80}}}`, "",
		`data: [DONE]`, "",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newOpenAI(openaiProfile(srv.URL))
	done := findDone(t, drain(p.Stream(context.Background(), "sys", nil, nil)))
	if done.Usage == nil {
		t.Fatal("Usage should not be nil")
	}
	u := *done.Usage
	if u.InputTokens != 100 || u.OutputTokens != 7 {
		t.Errorf("usage = %+v, want 100/7", u)
	}
	if u.CacheReadTokens != 80 || u.CacheCreationTokens != 0 {
		t.Errorf("cache = read %d create %d, want 80/0", u.CacheReadTokens, u.CacheCreationTokens)
	}
	if !u.CacheUsageKnown {
		t.Error("CacheUsageKnown = false, want true")
	}
	if rate, ok := u.CacheHitRate(); !ok || rate != 0.8 {
		t.Errorf("CacheHitRate = %v, %v; want 0.8, true", rate, ok)
	}
}

func TestUsageUnknownAndAdd(t *testing.T) {
	unknown := Usage{InputTokens: 10}
	if _, ok := unknown.CacheHitRate(); ok {
		t.Error("unknown cache usage should not produce a hit rate")
	}

	u := Usage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 60, CacheCreationTokens: 10, CacheUsageKnown: true}
	v := Usage{InputTokens: 50, OutputTokens: 5, CacheReadTokens: 20, CacheUsageKnown: true}
	got := u.Add(v)
	if got.InputTokens != 150 || got.OutputTokens != 15 || got.CacheReadTokens != 80 || got.CacheCreationTokens != 10 || !got.CacheUsageKnown {
		t.Errorf("Add = %+v", got)
	}
}

func TestNormalizeStopReasons(t *testing.T) {
	anthropicCases := map[string]string{
		"end_turn":      StopEndTurn,
		"stop_sequence": StopEndTurn,
		"max_tokens":    StopMaxTokens,
		"tool_use":      StopToolUse,
		"":              "",
		"weird":         StopEndTurn,
	}
	for in, want := range anthropicCases {
		if got := normalizeAnthropicStop(in); got != want {
			t.Errorf("normalizeAnthropicStop(%q) = %q, want %q", in, got, want)
		}
	}

	openaiCases := map[string]string{
		"stop":           StopEndTurn,
		"content_filter": StopEndTurn,
		"length":         StopMaxTokens,
		"tool_calls":     StopToolUse,
		"function_call":  StopToolUse,
		"":               "",
		"weird":          StopEndTurn,
	}
	for in, want := range openaiCases {
		if got := normalizeOpenAIStop(in); got != want {
			t.Errorf("normalizeOpenAIStop(%q) = %q, want %q", in, got, want)
		}
	}
}
