package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindFiles(t *testing.T) {
	root, r := setup(t)
	os.WriteFile(filepath.Join(root, "x.go"), []byte("package x"), 0o644)
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "sub/y.go"), []byte("package y"), 0o644)
	os.WriteFile(filepath.Join(root, "z.txt"), []byte("t"), 0o644)
	res := r.Execute(context.Background(), "find_files", map[string]any{"pattern": "**/*.go"})
	if !res.OK || !strings.Contains(res.Content, "x.go") || !strings.Contains(res.Content, filepath.Join("sub", "y.go")) {
		t.Errorf("find = %+v", res)
	}
	if strings.Contains(res.Content, "z.txt") {
		t.Errorf("find should not match z.txt: %+v", res)
	}
}

func TestSearchCode(t *testing.T) {
	root, r := setup(t)
	os.WriteFile(filepath.Join(root, "a.go"), []byte("func Foo() {}\nvar bar int"), 0o644)
	res := r.Execute(context.Background(), "search_code", map[string]any{"pattern": "func \\w+"})
	if !res.OK || !strings.Contains(res.Content, "a.go:1:") || !strings.Contains(res.Content, "Foo") {
		t.Errorf("search = %+v", res)
	}
	if res := r.Execute(context.Background(), "search_code", map[string]any{"pattern": "["}); res.OK {
		t.Error("invalid regex should fail")
	}
}
