package tool

import "testing"

func setup(t *testing.T) (string, *Registry) {
	t.Helper()
	root := t.TempDir()
	r := NewRegistry(root)
	RegisterAll(r, root)
	return root, r
}

func TestRegisterAllSix(t *testing.T) {
	_, r := setup(t)
	if len(r.Definitions()) != 6 {
		t.Fatalf("want 6 tools, got %d", len(r.Definitions()))
	}
	for _, name := range []string{"read_file", "write_file", "edit_file", "run_command", "find_files", "search_code"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("missing tool %s", name)
		}
	}
}
