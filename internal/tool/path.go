package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// argString 从 args 取字符串参数，缺失或类型不符返回错误。
func argString(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("缺少参数 %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("参数 %q 应为字符串", key)
	}
	return s, nil
}

// resolvePath 把工具给的相对/绝对路径解析进 root，返回可用于读写的绝对路径。
// 越界（../、root 外绝对路径、软链指向 root 外）返回错误。
func resolvePath(root, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// 规范化 root 自身的软链（如 macOS /var → /private/var），
	// 保证后续与目标路径在同一坐标系下做前缀比较。
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("无法解析工作目录 %q: %v", root, err)
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(absRoot, p)
	}
	// 解析目标路径中「已存在部分」的软链（write 新文件时末尾几段可能尚不存在）。
	abs, err = evalExisting(abs)
	if err != nil {
		return "", fmt.Errorf("无法解析路径 %q: %v", p, err)
	}
	// 前缀校验：解析后的 abs 必须在 absRoot 之内（或等于）。
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil {
		return "", fmt.Errorf("无法解析路径 %q", p)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越界，仅允许工作目录内: %q", p)
	}
	return abs, nil
}

// evalExisting 解析 path 中「已存在的最深祖先」的软链，再把不存在的后缀拼回。
// 既能拦 root 内软链指向外部，又允许 write 一个尚不存在的文件/目录。
func evalExisting(path string) (string, error) {
	var suffix []string
	cur := path
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur { // 已到文件系统根仍未找到已存在祖先
			return "", fmt.Errorf("路径不存在: %q", path)
		}
		suffix = append([]string{filepath.Base(cur)}, suffix...)
		cur = parent
	}
	resolved, err := filepath.EvalSymlinks(cur)
	if err != nil {
		return "", err
	}
	if len(suffix) == 0 {
		return resolved, nil
	}
	parts := append([]string{resolved}, suffix...)
	return filepath.Join(parts...), nil
}
