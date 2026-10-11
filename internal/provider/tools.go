package provider

import (
	"encoding/json"
	"strings"

	"nekocode/internal/message"
)

// parseToolCall 把拼接完成的参数 JSON 解析成 message.ToolCall。
// 空字符串视为无参数；解析失败则 Input 为空 map（由后续工具执行报参数错误）。
func parseToolCall(id, name, argsJSON string) message.ToolCall {
	input := map[string]any{}
	if s := strings.TrimSpace(argsJSON); s != "" {
		_ = json.Unmarshal([]byte(s), &input)
	}
	return message.ToolCall{ID: id, Name: name, Input: input}
}
