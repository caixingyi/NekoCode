package agent

import (
	"fmt"

	"nekocode/internal/message"
)

// planBlockNote 是 plan 模式下拦截写类工具时回灌给模型的结果文案（R15）。
// 必须同时说清三件事：为什么被拦、不要重试、该做什么——否则模型容易反复撞同一面墙，
// 把轮数白烧掉（好在被拦的调用也计入重复检测，撞三次就会被中止）。
const planBlockNote = "当前处于计划模式（plan-only），写操作已被阻止：%s 未执行。" +
	"请不要重试任何写类工具，直接输出一份完整的实施计划（步骤、涉及的文件、验证方式），交由用户审批。"

// planBlockedResult 为被计划模式拦截的调用合成结构化结果（R15）。
func planBlockedResult(call message.ToolCall) message.ToolResult {
	return message.ToolResult{
		ID:      call.ID,
		Content: fmt.Sprintf(planBlockNote, call.Name),
		IsError: true,
	}
}
