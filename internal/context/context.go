// Package context 负责对话上下文的 token 近似估算与摘要压缩，
// 在接近 context_window 时自动把旧消息压缩成摘要消息。
package context

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/provider"
)

// 估算与摘要的默认参数（均可由 spec 的开放问题确认后调整）。
const (
	// DefaultCharsPerToken：1 token ≈ 4 字符（R22）。
	DefaultCharsPerToken = 4
	// SummariseThreshold：达到 context_window 的该比例时触发自动摘要（R23）。
	SummariseThreshold = 0.8
	// KeepRecentRounds：摘要时保留的最近对话轮数（一轮 = user+assistant）。
	KeepRecentRounds = 6
	// FallbackContextWindow：无法确定模型上下文窗口时的保守默认。
	FallbackContextWindow = 8000
)

// summariseSystemPrompt 是摘要请求使用的 system prompt。
const summariseSystemPrompt = "你是对话摘要助手。请把以下对话历史压缩成一段简洁的中文摘要，" +
	"保留关键事实、决策与未完成的话题，不要添加新信息，不要与用户对话。"

// Estimate 按字符数近似估算 token 用量（R22）。
// 计入 system 文本、每条消息的 Content 与 Thinking。
func Estimate(system string, msgs []message.Message) int {
	total := len([]rune(system))
	for _, m := range msgs {
		total += len([]rune(m.Content))
		total += len([]rune(m.Thinking))
	}
	if total == 0 {
		return 0
	}
	return total / DefaultCharsPerToken
}

// ContextWindow 解析有效上下文窗口：profile 配置 > 模型默认表 > 兜底（R22）。
func ContextWindow(p config.Profile) int {
	if p.ContextWindow > 0 {
		return p.ContextWindow
	}
	m := strings.ToLower(p.Model)
	switch {
	case strings.HasPrefix(m, "claude"):
		return 200000
	case strings.HasPrefix(m, "gpt-4.1"):
		return 1047576
	case strings.HasPrefix(m, "gpt-4o"):
		return 128000
	case strings.HasPrefix(m, "gpt-4"):
		return 8192
	case strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return 200000
	case strings.HasPrefix(m, "deepseek"):
		return 64000
	default:
		return FallbackContextWindow
	}
}

// ShouldSummarize 判断当前是否达到自动摘要阈值（不实际压缩）。
func ShouldSummarize(p config.Profile, msgs []message.Message) bool {
	est := Estimate(p.EffectiveSystemPrompt(), msgs)
	return est >= int(float64(ContextWindow(p))*SummariseThreshold)
}

// Summarize 在达到阈值时（或 force 时）把旧消息压缩成一条摘要消息，
// 保留最近 KeepRecentRounds 轮（R23、R24、R25）。
// 返回新的消息切片与被压缩的消息数；未触发时原样返回、计数为 0。
// 摘要请求失败时返回错误，不修改原消息（R8 边界：失败回退）。
func Summarize(ctx context.Context, prov provider.Provider, p config.Profile, msgs []message.Message, force bool) ([]message.Message, int, error) {
	if !force && !ShouldSummarize(p, msgs) {
		return msgs, 0, nil
	}

	keep := KeepRecentRounds * 2
	if len(msgs) <= keep {
		// 消息少于保留轮数，不触发摘要（R22 边界）。
		return msgs, 0, nil
	}

	toSummarize := msgs[:len(msgs)-keep]
	recent := msgs[len(msgs)-keep:]

	transcript := buildTranscript(toSummarize)
	ch := prov.Stream(ctx, summariseSystemPrompt, []message.Message{
		{Role: message.RoleUser, Content: transcript, CreatedAt: time.Now()},
	})

	var summary string
	var streamErr error
	for ev := range ch {
		switch ev.Type {
		case provider.EventText:
			summary += ev.Text
		case provider.EventError:
			streamErr = ev.Err
		}
	}
	if streamErr != nil {
		return nil, 0, fmt.Errorf("摘要请求失败: %w", streamErr)
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil, 0, errors.New("摘要结果为空")
	}

	out := make([]message.Message, 0, len(recent)+1)
	out = append(out, message.Message{
		Role:      message.RoleSystem,
		Content:   summary,
		IsSummary: true,
		CreatedAt: time.Now(),
	})
	out = append(out, recent...)
	return out, len(toSummarize), nil
}

// buildTranscript 把待压缩的消息格式化为纯文本供摘要。
func buildTranscript(msgs []message.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case message.RoleUser:
			b.WriteString("[用户]\n")
			b.WriteString(m.Content)
		case message.RoleAssistant:
			b.WriteString("[助手]\n")
			b.WriteString(m.Content)
			if m.Thinking != "" {
				b.WriteString("\n[思考]\n")
				b.WriteString(m.Thinking)
			}
		case message.RoleSystem:
			b.WriteString("[摘要]\n")
			b.WriteString(m.Content)
		}
		b.WriteString("\n\n")
	}
	return b.String()
}
