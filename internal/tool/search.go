package tool

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxSearchHits = 200

// readFileForSearch 读取文件用于搜索；跳过疑似二进制（含 NUL 字节）与过大文件。
func readFileForSearch(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > maxOutputBytes*4 {
		data = data[:maxOutputBytes*4]
	}
	if strings.IndexByte(string(data), 0) >= 0 {
		return "", os.ErrInvalid // 二进制，跳过
	}
	return string(data), nil
}

type findFiles struct{ root string }

func (t findFiles) Name() string { return "find_files" }
func (t findFiles) Description() string {
	return "按 glob 模式（如 **/*.go）在工作目录下查找文件路径。"
}
func (t findFiles) NeedsConfirm() bool { return false }
func (t findFiles) IsReadOnly() bool   { return true }
func (t findFiles) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "glob 模式，如 *.go 或 **/*.md"},
		},
		"required": []string{"pattern"},
	}
}
func (t findFiles) Execute(_ context.Context, args map[string]any) Result {
	pattern, err := argString(args, "pattern")
	if err != nil {
		return fail("%v", err)
	}
	var hits []string
	err = filepath.WalkDir(t.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(t.root, path)
		if matchGlob(pattern, rel) {
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		return fail("查找失败: %v", err)
	}
	if len(hits) == 0 {
		return ok("没有匹配 " + pattern + " 的文件")
	}
	if len(hits) > maxSearchHits {
		hits = hits[:maxSearchHits]
	}
	return ok(strings.Join(hits, "\n"))
}

// matchGlob 支持 ** 跨目录匹配；否则退回按 base name 或整路径的 filepath.Match。
func matchGlob(pattern, rel string) bool {
	if strings.Contains(pattern, "**") {
		suffix := strings.TrimPrefix(pattern, "**/")
		if ok, _ := filepath.Match(suffix, filepath.Base(rel)); ok {
			return true
		}
		if ok, _ := filepath.Match(suffix, rel); ok {
			return true
		}
		return false
	}
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	ok, _ := filepath.Match(pattern, filepath.Base(rel))
	return ok
}

type searchCode struct{ root string }

func (t searchCode) Name() string { return "search_code" }
func (t searchCode) Description() string {
	return "用正则在工作目录的文件内容里搜索，返回 文件:行号:内容。"
}
func (t searchCode) NeedsConfirm() bool { return false }
func (t searchCode) IsReadOnly() bool   { return true }
func (t searchCode) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "正则表达式"},
		},
		"required": []string{"pattern"},
	}
}
func (t searchCode) Execute(_ context.Context, args map[string]any) Result {
	pattern, err := argString(args, "pattern")
	if err != nil {
		return fail("%v", err)
	}
	re, err := regexp.Compile(pattern) // 把正则字符串编译成一个正则对象，后续进行匹配使用这个对象的方法
	if err != nil {
		return fail("正则无效: %v", err)
	}
	var hits []string
	// 遍历工作目录下的所有文件，查找匹配正则的行
	_ = filepath.WalkDir(t.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if err == nil && d.IsDir() && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(hits) >= maxSearchHits {
			return filepath.SkipAll
		}
		// 读取文件内容并对最大行数进行限制，避免超大文件导致性能问题，同时
		// 跳过二进制文件（通过尝试读取为 UTF-8 文本来判断）。
		data, rerr := readFileForSearch(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(t.root, path)
		for i, line := range strings.Split(data, "\n") {
			if re.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d:%s", rel, i+1, strings.TrimSpace(line)))
				if len(hits) >= maxSearchHits {
					break
				}
			}
		}
		return nil
	})
	if len(hits) == 0 {
		return ok("没有匹配 " + pattern + " 的内容")
	}
	return ok(strings.Join(hits, "\n"))
}
