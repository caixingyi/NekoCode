package tui

import "nekocode/internal/message"

// realMessages 去掉系统补充消息，便于测试按真实对话断言。
func realMessages(msgs []message.Message) []message.Message {
	out := make([]message.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.IsSupplement {
			continue
		}
		out = append(out, m)
	}
	return out
}
