package environment

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseGitStatus(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want Info
	}{
		{
			name: "normal with counts",
			out: "## main...origin/main [ahead 1]\n" +
				" M file.go\n" +
				"M  staged.go\n" +
				"A  added.go\n" +
				"?? new.txt\n" +
				" D deleted.go\n",
			want: Info{Branch: "main", Dirty: true, Staged: 2, Modified: 2, Untracked: 1},
		},
		{
			name: "detached head",
			out:  "## HEAD (no branch)\n",
			want: Info{Branch: "detached HEAD"},
		},
		{
			name: "no commits yet",
			out:  "## No commits yet on main\nA  file.go\n",
			want: Info{Branch: "main", Dirty: true, Staged: 1},
		},
		{
			name: "clean",
			out:  "## main\n",
			want: Info{Branch: "main"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := Info{GitKnown: true, IsRepo: true}
			parseGitStatus(c.out, &info)
			if info.Branch != c.want.Branch {
				t.Errorf("branch = %q, want %q", info.Branch, c.want.Branch)
			}
			if info.Dirty != c.want.Dirty {
				t.Errorf("dirty = %v, want %v", info.Dirty, c.want.Dirty)
			}
			if info.Staged != c.want.Staged || info.Modified != c.want.Modified || info.Untracked != c.want.Untracked {
				t.Errorf("counts = staged %d modified %d untracked %d, want %d/%d/%d",
					info.Staged, info.Modified, info.Untracked,
					c.want.Staged, c.want.Modified, c.want.Untracked)
			}
		})
	}
}

func TestCollectNonRepo(t *testing.T) {
	dir := t.TempDir()
	info := Collect(context.Background(), dir)
	if info.WorkDir != dir {
		t.Errorf("workdir = %q, want %q", info.WorkDir, dir)
	}
	if info.OS == "" || info.Arch == "" || info.Time.IsZero() {
		t.Errorf("missing basic environment: %+v", info)
	}
	if !info.GitKnown {
		t.Skipf("git unavailable: %s", info.GitReason)
	}
	if info.IsRepo {
		t.Errorf("temp dir should not be a git repo: %+v", info)
	}
	if info.GitReason != "" {
		t.Errorf("non-repo should leave GitReason empty, got %q", info.GitReason)
	}
}

func TestCollectRepo(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := Collect(context.Background(), dir)
	if !info.GitKnown || !info.IsRepo {
		t.Fatalf("expected repo snapshot: %+v", info)
	}
	if info.Branch == "" {
		t.Errorf("branch should not be empty: %+v", info)
	}
	if !info.Dirty || info.Untracked != 1 {
		t.Errorf("expected one untracked file: %+v", info)
	}
}
