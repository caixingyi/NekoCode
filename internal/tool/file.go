package tool

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxReadLines = 2000

func itoa(n int) string { return strconv.Itoa(n) }

type readFile struct{ root string }

func (t readFile) Name() string        { return "read_file" }
func (t readFile) Description() string { return "读取文件内容。超大文件按行截断。" }
func (t readFile) NeedsConfirm() bool  { return false }
func (t readFile) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "相对工作目录的文件路径"},
		},
		"required": []string{"path"},
	}
}
func (t readFile) Execute(_ context.Context, args map[string]any) Result {
	p, err := argString(args, "path")
	if err != nil {
		return fail("%v", err)
	}
	abs, err := resolvePath(t.root, p)
	if err != nil {
		return fail("%v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return fail("读取失败: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxReadLines {
		lines = lines[:maxReadLines]
		return ok(strings.Join(lines, "\n") + "\n... [已截断，仅显示前 " + itoa(maxReadLines) + " 行]")
	}
	return ok(string(data))
}

type writeFile struct{ root string }

func (t writeFile) Name() string        { return "write_file" }
func (t writeFile) Description() string { return "写入/覆盖文件（自动创建父目录）。" }
func (t writeFile) NeedsConfirm() bool  { return true }
func (t writeFile) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "相对工作目录的文件路径"},
			"content": map[string]any{"type": "string", "description": "文件内容"},
		},
		"required": []string{"path", "content"},
	}
}
func (t writeFile) Execute(_ context.Context, args map[string]any) Result {
	p, err := argString(args, "path")
	if err != nil {
		return fail("%v", err)
	}
	content, err := argString(args, "content")
	if err != nil {
		return fail("%v", err)
	}
	abs, err := resolvePath(t.root, p)
	if err != nil {
		return fail("%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fail("创建目录失败: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return fail("写入失败: %v", err)
	}
	return ok("已写入 " + p + "（" + itoa(len(content)) + " 字节）")
}

type editFile struct{ root string }

func (t editFile) Name() string { return "edit_file" }
func (t editFile) Description() string {
	return "把文件中唯一匹配的 old_string 替换为 new_string。old_string 必须在文件中恰好出现一次。"
}
func (t editFile) NeedsConfirm() bool { return true }
func (t editFile) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":       map[string]any{"type": "string", "description": "相对工作目录的文件路径"},
			"old_string": map[string]any{"type": "string", "description": "要替换的原文（须唯一匹配）"},
			"new_string": map[string]any{"type": "string", "description": "替换后的新文本"},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}
func (t editFile) Execute(_ context.Context, args map[string]any) Result {
	p, err := argString(args, "path")
	if err != nil {
		return fail("%v", err)
	}
	oldStr, err := argString(args, "old_string")
	if err != nil {
		return fail("%v", err)
	}
	newStr, err := argString(args, "new_string")
	if err != nil {
		return fail("%v", err)
	}
	abs, err := resolvePath(t.root, p)
	if err != nil {
		return fail("%v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return fail("读取失败: %v", err)
	}
	n := strings.Count(string(data), oldStr)
	if n == 0 {
		return fail("old_string 在文件中未找到，请核对原文后重试")
	}
	if n > 1 {
		return fail("old_string 匹配到 %d 处，需唯一匹配。请提供更长、更具体的上下文", n)
	}
	updated := strings.Replace(string(data), oldStr, newStr, 1)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return fail("写入失败: %v", err)
	}
	return ok("已编辑 " + p)
}
