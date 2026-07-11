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

// anthropicVersion 是 Anthropic Messages API 的版本头。
const anthropicVersion = "2023-06-01"

// anthropicProvider 实现 Anthropic Claude 的流式对话（R8、R10）。
type anthropicProvider struct {
	profile config.Profile
}

// newAnthropic 构造 Anthropic provider。
func newAnthropic(p config.Profile) (Provider, error) {
	return &anthropicProvider{profile: p}, nil
}

// blockState 跟踪单个 content block 的状态（仅 thinking block 需要保存签名）。
type blockState struct {
	isThinking bool
	thinking   string
	signature  string
}

// anthropicDelta 是 content_block_delta 中的 delta 字段。
type anthropicDelta struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

// Stream 发起一次 Anthropic 流式对话（R8）。
func (a *anthropicProvider) Stream(ctx context.Context, system string, msgs []message.Message) <-chan StreamEvent {
	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)

		// ctx 取消后发送可能阻塞，统一用 send 守卫。
		send := func(ev StreamEvent) {
			select {
			case ch <- ev:
			case <-ctx.Done():
			}
		}

		body, err := message.Normalize(a.profile, system, msgs)
		if err != nil {
			send(StreamEvent{Type: EventError, Err: err})
			return
		}
		payload, err := json.Marshal(body)
		if err != nil {
			send(StreamEvent{Type: EventError, Err: err})
			return
		}

		url := strings.TrimRight(a.profile.EffectiveBaseURL(), "/") + "/v1/messages"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			send(StreamEvent{Type: EventError, Err: err})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.profile.APIKey)
		req.Header.Set("anthropic-version", anthropicVersion)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			send(StreamEvent{Type: EventError, Err: err})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			send(StreamEvent{Type: EventError, Err: fmt.Errorf("anthropic API %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))})
			return
		}

		blocks := map[int]*blockState{}
		rerr := readSSE(resp.Body, func(ev sseEvent) error {
			data := []byte(ev.Data)
			switch ev.Event {
			case "content_block_start":
				var e struct {
					Index        int `json:"index"`
					ContentBlock struct {
						Type     string `json:"type"`
						Thinking string `json:"thinking"`
					} `json:"content_block"`
				}
				_ = json.Unmarshal(data, &e)
				if e.ContentBlock.Type == "thinking" {
					blocks[e.Index] = &blockState{isThinking: true, thinking: e.ContentBlock.Thinking}
					if e.ContentBlock.Thinking != "" {
						send(StreamEvent{Type: EventThinking, Text: e.ContentBlock.Thinking})
					}
				}
			case "content_block_delta":
				var e struct {
					Index int             `json:"index"`
					Delta anthropicDelta  `json:"delta"`
				}
				_ = json.Unmarshal(data, &e)
				switch e.Delta.Type {
				case "text_delta":
					send(StreamEvent{Type: EventText, Text: e.Delta.Text})
				case "thinking_delta":
					if b := blocks[e.Index]; b != nil {
						b.thinking += e.Delta.Thinking
					}
					send(StreamEvent{Type: EventThinking, Text: e.Delta.Thinking})
				case "signature_delta":
					if b := blocks[e.Index]; b != nil {
						b.signature += e.Delta.Signature
					}
				}
			case "content_block_stop":
				var e struct {
					Index int `json:"index"`
				}
				_ = json.Unmarshal(data, &e)
				if b := blocks[e.Index]; b != nil && b.isThinking && b.signature != "" {
					// 交付该 thinking block 的签名（Text 为空）。
					send(StreamEvent{Type: EventThinking, Signature: b.signature})
				}
			case "message_stop":
				send(StreamEvent{Type: EventDone})
				return io.EOF // 正常结束，停止 readSSE
			case "error":
				var e struct {
					Error struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					} `json:"error"`
				}
				_ = json.Unmarshal(data, &e)
				msg := e.Error.Message
				if msg == "" {
					msg = "anthropic stream error"
				}
				send(StreamEvent{Type: EventError, Err: errors.New(msg)})
				return io.EOF
			}
			return nil
		})

		// readSSE 因 message_stop/error 返回 io.EOF 属正常；其它错误为读取失败（如网络/取消）。
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			send(StreamEvent{Type: EventError, Err: rerr})
		}
	}()
	return ch
}
