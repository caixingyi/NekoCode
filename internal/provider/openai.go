package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nekocode/internal/config"
	"nekocode/internal/message"
)

// openaiProvider 实现 OpenAI（及兼容端点）的流式对话（R8、R10）。
type openaiProvider struct {
	profile config.Profile
}

// newOpenAI 构造 OpenAI provider。
func newOpenAI(p config.Profile) (Provider, error) {
	return &openaiProvider{profile: p}, nil
}

// openaiChunk 是 OpenAI 流式响应的单个 chunk。
type openaiChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// Stream 发起一次 OpenAI 流式对话（R8）。thinking 配置被忽略（R10）。
func (o *openaiProvider) Stream(ctx context.Context, system string, msgs []message.Message) <-chan StreamEvent {
	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)

		send := func(ev StreamEvent) {
			select {
			case ch <- ev:
			case <-ctx.Done():
			}
		}

		// 构建请求体，包含 system + 历史消息。
		body, err := message.Normalize(o.profile, system, msgs)
		if err != nil {
			send(StreamEvent{Type: EventError, Err: err})
			return
		}
		payload, err := json.Marshal(body)
		if err != nil {
			send(StreamEvent{Type: EventError, Err: err})
			return
		}

		url := strings.TrimRight(o.profile.EffectiveBaseURL(), "/") + "/v1/chat/completions"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			send(StreamEvent{Type: EventError, Err: err})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+o.profile.APIKey)

		// 发送请求，处理 SSE 流。
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			send(StreamEvent{Type: EventError, Err: err})
			return
		}
		defer resp.Body.Close()

		// err = nil 不代表流式对话成功，OpenAI 可能返回 4xx/5xx 错误码。
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			send(StreamEvent{Type: EventError, Err: fmt.Errorf("openai API %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))})
			return
		}

		done := false
		rerr := readSSE(resp.Body, func(ev sseEvent) error {
			if ev.Data == "[DONE]" {
				done = true
				send(StreamEvent{Type: EventDone})
				return io.EOF // 正常结束
			}
			var chunk openaiChunk
			_ = json.Unmarshal([]byte(ev.Data), &chunk)
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				send(StreamEvent{Type: EventText, Text: chunk.Choices[0].Delta.Content})
			}
			return nil
		})

		if rerr != nil && !errors.Is(rerr, io.EOF) {
			send(StreamEvent{Type: EventError, Err: rerr})
		} else if rerr == nil && !done {
			// 流自然结束但未收到 [DONE]，补发 EventDone。
			send(StreamEvent{Type: EventDone})
		}
	}()
	return ch
}
