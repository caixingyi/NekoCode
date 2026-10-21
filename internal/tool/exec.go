package tool

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

const maxOutputBytes = 30 * 1024

func truncateOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + "\n... [输出已截断]"
}

type runCommand struct{ root string }

func (t runCommand) Name() string { return "run_command" }
func (t runCommand) Description() string {
	return "在工作目录下用 sh -c 执行 shell 命令，返回合并的 stdout/stderr 与退出码。"
}
func (t runCommand) NeedsConfirm() bool { return true }

// IsReadOnly 恒为 false：shell 命令既能 git status 也能 rm -rf，静态无法区分，按最坏情况归类。
func (t runCommand) IsReadOnly() bool { return false }
func (t runCommand) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "要执行的 shell 命令"},
		},
		"required": []string{"command"},
	}
}
func (t runCommand) Execute(ctx context.Context, args map[string]any) Result {
	cmdStr, err := argString(args, "command")
	if err != nil {
		return fail("%v", err)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = t.root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	out := truncateOutput(buf.String())
	if ctx.Err() != nil {
		return fail("命令超时或被取消。已捕获输出:\n%s", out)
	}
	code := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return fail("命令启动失败: %v", runErr)
		}
	}
	return ok(fmt.Sprintf("退出码: %d\n%s", code, out))
}
