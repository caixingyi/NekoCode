package tool

import (
	"context"
	"strings"
	"testing"
)

func TestRunCommand(t *testing.T) {
	_, r := setup(t)
	res := r.Execute(context.Background(), "run_command", map[string]any{"command": "echo hello"})
	if !res.OK || !strings.Contains(res.Content, "hello") || !strings.Contains(res.Content, "退出码: 0") {
		t.Errorf("run = %+v", res)
	}
	// 非零退出仍是成功执行，结果里体现失败
	res = r.Execute(context.Background(), "run_command", map[string]any{"command": "exit 3"})
	if !res.OK || !strings.Contains(res.Content, "退出码: 3") {
		t.Errorf("run exit3 = %+v", res)
	}
}
