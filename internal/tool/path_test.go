package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePathWithinRoot(t *testing.T) {
	root := t.TempDir()
	abs, err := resolvePath(root, "sub/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	// root 经 EvalSymlinks 规范化（如 macOS /var → /private/var），比较也要用规范化后的 root。
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wantRoot, "sub/a.txt"); abs != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}
}

func TestResolvePathEscapeRejected(t *testing.T) {
	root := t.TempDir()
	cases := []string{"../evil.txt", "sub/../../evil.txt", "/etc/passwd"}
	for _, p := range cases {
		if _, err := resolvePath(root, p); err == nil {
			t.Errorf("resolvePath(%q) should be rejected", p)
		}
	}
}

func TestResolvePathAbsWithinRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "x.txt")
	abs, err := resolvePath(root, inside)
	if err != nil {
		t.Fatalf("abs path within root should be allowed: %v", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(root)
	if want := filepath.Join(wantRoot, "x.txt"); abs != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}
}

func TestResolvePathSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	// link/passwd 表面上在 root 内，实际指向 outside，必须被拒。
	if _, err := resolvePath(root, filepath.Join("link", "passwd")); err == nil {
		t.Error("symlink pointing outside root should be rejected")
	}
	// 直接读 link 本身也应被拒（link 指向 outside）。
	if _, err := resolvePath(root, "link"); err == nil {
		t.Error("symlink itself pointing outside should be rejected")
	}
}

func TestResolvePathSymlinkWithinRootAllowed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	abs, err := resolvePath(root, filepath.Join("link", "a.txt"))
	if err != nil {
		t.Fatalf("symlink within root should be allowed: %v", err)
	}
	realAbs, _ := filepath.EvalSymlinks(filepath.Join(root, "real"))
	if !strings.HasPrefix(abs, realAbs) {
		t.Errorf("abs = %q, want under %q", abs, realAbs)
	}
}
