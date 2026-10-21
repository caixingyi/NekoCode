package agent

import (
	"context"
	"fmt"
	"strings"

	"nekocode/internal/message"
)

// Decision 是用户对一次写类工具调用的决定。
type Decision int

const (
	// DecisionDeny 拒绝执行当前工具。
	DecisionDeny Decision = iota
	// DecisionAllow 允许执行当前工具。
	DecisionAllow
	// DecisionAllowAll 允许本次循环内后续所有写类工具。
	DecisionAllowAll
)

// Confirmer 由消费方实现，用于在执行写类工具前向用户确认（R13）。
//
// Confirm 会阻塞直到用户作答或 ctx 被取消；preview 是 Describe 生成的预览文本，
// 供界面直接展示。实现方应在 ctx 取消时尽快返回 ctx.Err()，避免 agent goroutine 挂死。
type Confirmer interface {
	Confirm(ctx context.Context, call message.ToolCall, preview string) (Decision, error)
}

// Describe 生成一次工具调用的确认预览（沿用 step 2 的呈现方式，R11）。
// 放在 agent 包是为了让确认接口自带可展示内容，消费方无需重复实现。
func Describe(call message.ToolCall) string {
	switch call.Name {
	case "run_command":
		return "$ " + argStr(call.Input, "command")
	case "write_file":
		return "写入 " + argStr(call.Input, "path") + "\n" + previewText(argStr(call.Input, "content"))
	case "edit_file":
		return "编辑 " + argStr(call.Input, "path") +
			"\n- " + oneLine(argStr(call.Input, "old_string")) +
			"\n+ " + oneLine(argStr(call.Input, "new_string"))
	default:
		return fmt.Sprintf("%s %v", call.Name, call.Input)
	}
}

func argStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80]) + "…"
	}
	return s
}

func previewText(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "…")
	}
	return strings.Join(lines, "\n")
}
