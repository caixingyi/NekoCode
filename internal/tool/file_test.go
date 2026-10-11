package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile(t *testing.T) {
	root, r := setup(t)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\nworld"), 0o644)
	res := r.Execute(context.Background(), "read_file", map[string]any{"path": "a.txt"})
	if !res.OK || res.Content != "hello\nworld" {
		t.Errorf("read = %+v", res)
	}
	// 不存在
	if res := r.Execute(context.Background(), "read_file", map[string]any{"path": "nope"}); res.OK {
		t.Error("read missing file should fail")
	}
}

func TestWriteFile(t *testing.T) {
	root, r := setup(t)
	res := r.Execute(context.Background(), "write_file", map[string]any{"path": "sub/b.txt", "content": "hi"})
	if !res.OK {
		t.Fatalf("write = %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(root, "sub/b.txt"))
	if string(data) != "hi" {
		t.Errorf("content = %q", data)
	}
}

func TestEditFileUnique(t *testing.T) {
	root, r := setup(t)
	os.WriteFile(filepath.Join(root, "c.txt"), []byte("foo bar baz"), 0o644)
	res := r.Execute(context.Background(), "edit_file", map[string]any{"path": "c.txt", "old_string": "bar", "new_string": "BAR"})
	if !res.OK {
		t.Fatalf("edit = %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(root, "c.txt"))
	if string(data) != "foo BAR baz" {
		t.Errorf("content = %q", data)
	}
}

func TestEditFileNoMatchAndMulti(t *testing.T) {
	root, r := setup(t)
	os.WriteFile(filepath.Join(root, "d.txt"), []byte("x x x"), 0o644)
	if res := r.Execute(context.Background(), "edit_file", map[string]any{"path": "d.txt", "old_string": "z", "new_string": "Z"}); res.OK {
		t.Error("no match should fail")
	}
	if res := r.Execute(context.Background(), "edit_file", map[string]any{"path": "d.txt", "old_string": "x", "new_string": "Y"}); res.OK || !strings.Contains(res.Content, "3") {
		t.Errorf("multi match should fail with count: %+v", res)
	}
}

func TestToolsRejectEscape(t *testing.T) {
	_, r := setup(t)
	for _, name := range []string{"read_file", "write_file", "edit_file"} {
		args := map[string]any{"path": "../evil.txt", "content": "x", "old_string": "a", "new_string": "b"}
		if res := r.Execute(context.Background(), name, args); res.OK {
			t.Errorf("%s should reject ../ escape", name)
		}
	}
}
