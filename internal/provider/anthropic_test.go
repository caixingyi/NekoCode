package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nekocode/internal/config"
	"nekocode/internal/message"
)

func anthropicProfile(base string) config.Profile {
	return config.Profile{Protocol: config.ProtocolAnthropic, Model: "claude-test", APIKey: "k", BaseURL: base}
}

func drain(ch <-chan StreamEvent) []StreamEvent {
	var out []StreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func TestAnthropicStreamTextAndThinking(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hmm\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-abc\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("missing anthropic-version header")
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, err := newAnthropic(anthropicProfile(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	evs := drain(p.Stream(context.Background(), "sys", []message.Message{message.NewMessage(message.RoleUser, "hi")}))

	if !strings.Contains(gotBody, `"stream":true`) {
		t.Errorf("request body missing stream:true: %s", gotBody)
	}
	if !strings.Contains(gotBody, "claude-test") {
		t.Errorf("request body missing model: %s", gotBody)
	}

	var texts, thinks, sigs []string
	done, hasErr := false, false
	for _, ev := range evs {
		switch ev.Type {
		case EventText:
			texts = append(texts, ev.Text)
		case EventThinking:
			if ev.Text != "" {
				thinks = append(thinks, ev.Text)
			}
			if ev.Signature != "" {
				sigs = append(sigs, ev.Signature)
			}
		case EventDone:
			done = true
		case EventError:
			hasErr = true
		}
	}
	if hasErr {
		t.Fatalf("unexpected EventError")
	}
	if !done {
		t.Errorf("no EventDone")
	}
	if len(thinks) != 1 || thinks[0] != "hmm" {
		t.Errorf("thinks = %v", thinks)
	}
	if len(sigs) != 1 || sigs[0] != "sig-abc" {
		t.Errorf("sigs = %v", sigs)
	}
	if strings.Join(texts, "") != "Hello world" {
		t.Errorf("texts = %v", texts)
	}
}

func TestAnthropicStreamErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n"))
	}))
	defer srv.Close()

	p, _ := newAnthropic(anthropicProfile(srv.URL))
	evs := drain(p.Stream(context.Background(), "sys", nil))

	hasErr, hasDone := false, false
	for _, ev := range evs {
		switch ev.Type {
		case EventError:
			hasErr = true
			if !strings.Contains(ev.Err.Error(), "overloaded") {
				t.Errorf("err = %v", ev.Err)
			}
		case EventDone:
			hasDone = true
		}
	}
	if !hasErr {
		t.Errorf("no EventError")
	}
	if hasDone {
		t.Errorf("should not have EventDone on error")
	}
}

func TestAnthropicStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	p, _ := newAnthropic(anthropicProfile(srv.URL))
	evs := drain(p.Stream(context.Background(), "sys", nil))
	if len(evs) != 1 || evs[0].Type != EventError {
		t.Fatalf("evs = %+v", evs)
	}
	if !strings.Contains(evs[0].Err.Error(), "401") {
		t.Errorf("err = %v", evs[0].Err)
	}
}

func TestAnthropicStreamCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // 阻塞直到客户端断开
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	p, _ := newAnthropic(anthropicProfile(srv.URL))
	ch := p.Stream(ctx, "sys", nil)

	// 读到一个事件后取消。
	select {
	case ev := <-ch:
		if ev.Type != EventText {
			t.Errorf("first ev = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first event")
	}
	cancel()

	// channel 应在取消后关闭。
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("channel did not close after cancel")
		}
	}
}
