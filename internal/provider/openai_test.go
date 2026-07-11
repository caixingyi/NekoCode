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

func openaiProfile(base string) config.Profile {
	return config.Profile{Protocol: config.ProtocolOpenAI, Model: "gpt-test", APIKey: "k", BaseURL: base}
}

func TestOpenAIStreamText(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{"content":" world"}}]}`, "",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, "",
		`data: [DONE]`, "",
	}, "\n")

	var gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sse))
	}))
	defer srv.Close()

	p, _ := newOpenAI(openaiProfile(srv.URL))
	evs := drain(p.Stream(context.Background(), "sys", []message.Message{message.NewMessage(message.RoleUser, "hi")}))

	if gotAuth != "Bearer k" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"stream":true`) {
		t.Errorf("body missing stream:true: %s", gotBody)
	}
	if strings.Contains(gotBody, "thinking") {
		t.Errorf("body should not contain thinking: %s", gotBody)
	}

	var texts []string
	done, hasErr := false, false
	for _, ev := range evs {
		switch ev.Type {
		case EventText:
			texts = append(texts, ev.Text)
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
	if strings.Join(texts, "") != "Hello world" {
		t.Errorf("texts = %v", texts)
	}
}

func TestOpenAIStreamThinkingIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "thinking") {
			t.Errorf("body should not contain thinking config: %s", b)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	prof := openaiProfile(srv.URL)
	prof.Thinking = &config.ThinkingConfig{Enabled: true, BudgetTokens: 2048} // 应被忽略
	p, _ := newOpenAI(prof)
	evs := drain(p.Stream(context.Background(), "sys", nil))

	var texts []string
	done := false
	for _, ev := range evs {
		switch ev.Type {
		case EventText:
			texts = append(texts, ev.Text)
		case EventDone:
			done = true
		}
	}
	if !done || strings.Join(texts, "") != "ok" {
		t.Errorf("texts=%v done=%v", texts, done)
	}
}

func TestOpenAIStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	p, _ := newOpenAI(openaiProfile(srv.URL))
	evs := drain(p.Stream(context.Background(), "sys", nil))
	if len(evs) != 1 || evs[0].Type != EventError {
		t.Fatalf("evs = %+v", evs)
	}
	if !strings.Contains(evs[0].Err.Error(), "401") {
		t.Errorf("err = %v", evs[0].Err)
	}
}

func TestOpenAIStreamNoDoneSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// 发送内容后直接关闭连接，不发 [DONE]。
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	}))
	defer srv.Close()

	p, _ := newOpenAI(openaiProfile(srv.URL))
	evs := drain(p.Stream(context.Background(), "sys", nil))
	hasDone := false
	for _, ev := range evs {
		if ev.Type == EventDone {
			hasDone = true
		}
	}
	if !hasDone {
		t.Errorf("expected EventDone to be补发 when [DONE] missing")
	}
}

func TestOpenAIStreamCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	p, _ := newOpenAI(openaiProfile(srv.URL))
	ch := p.Stream(ctx, "sys", nil)

	select {
	case ev := <-ch:
		if ev.Type != EventText {
			t.Errorf("first ev = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first event")
	}
	cancel()

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
