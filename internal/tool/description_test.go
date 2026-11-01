package tool

import (
	"strings"
	"testing"

	"nekocode/internal/prompt"
)

func TestToolRulesDoubleReinforced(t *testing.T) {
	global := prompt.New("").System()
	for _, want := range []string{"read_file", "search_code", "find_files", "run_command", "编辑前", "ls", "pwd"} {
		if !strings.Contains(global, want) {
			t.Errorf("global prompt missing %q", want)
		}
	}

	cases := []struct {
		name  string
		desc  string
		wants []string
	}{
		{"read_file", readFile{}.Description(), []string{"编辑前"}},
		{"write_file", writeFile{}.Description(), []string{"edit_file"}},
		{"edit_file", editFile{}.Description(), []string{"read_file", "唯一"}},
		{"search_code", searchCode{}.Description(), []string{"run_command", "grep"}},
		{"find_files", findFiles{}.Description(), []string{"run_command", "find"}},
		{"run_command", runCommand{}.Description(), []string{"专用工具", "cat", "grep", "pwd"}},
	}
	for _, c := range cases {
		for _, want := range c.wants {
			if !strings.Contains(c.desc, want) {
				t.Errorf("%s description missing %q: %q", c.name, want, c.desc)
			}
		}
	}
}

func TestRegistryRoot(t *testing.T) {
	r := NewRegistry("/tmp/project")
	if got := r.Root(); got != "/tmp/project" {
		t.Errorf("Root = %q, want /tmp/project", got)
	}
	var nilReg *Registry
	if got := nilReg.Root(); got != "" {
		t.Errorf("nil Root = %q, want empty", got)
	}
}
