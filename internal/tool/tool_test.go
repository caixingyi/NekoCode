package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeTool struct {
	name     string
	confirm  bool
	readOnly bool
	fn       func(context.Context, map[string]any) Result
}

func (f fakeTool) Name() string           { return f.name }
func (f fakeTool) Description() string    { return "fake " + f.name }
func (f fakeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (f fakeTool) NeedsConfirm() bool     { return f.confirm }
func (f fakeTool) IsReadOnly() bool       { return f.readOnly }
func (f fakeTool) Execute(ctx context.Context, args map[string]any) Result {
	return f.fn(ctx, args)
}

func TestRegistryRegisterGetDefinitions(t *testing.T) {
	r := NewRegistry(t.TempDir())
	r.Register(fakeTool{name: "b", fn: func(context.Context, map[string]any) Result { return ok("") }})
	r.Register(fakeTool{name: "a", fn: func(context.Context, map[string]any) Result { return ok("") }})

	if _, found := r.Get("a"); !found {
		t.Error("Get(a) not found")
	}
	if _, found := r.Get("nope"); found {
		t.Error("Get(nope) should not be found")
	}
	defs := r.Definitions()
	if len(defs) != 2 || defs[0].Name != "a" || defs[1].Name != "b" {
		t.Errorf("Definitions not sorted: %+v", defs)
	}
}

func TestRegistryExecuteUnknown(t *testing.T) {
	r := NewRegistry(t.TempDir())
	res := r.Execute(context.Background(), "nope", nil)
	if res.OK {
		t.Error("unknown tool should fail")
	}
}

// TestRegistryExecuteRecoversPanic：工具内部 panic 必须转成结构化失败，不能炸掉进程（R12）。
func TestRegistryExecuteRecoversPanic(t *testing.T) {
	r := NewRegistry(t.TempDir())
	r.Register(fakeTool{name: "boom", fn: func(context.Context, map[string]any) Result {
		panic("kaboom")
	}})
	res := r.Execute(context.Background(), "boom", nil)
	if res.OK {
		t.Error("panicking tool should return failure")
	}
	if !strings.Contains(res.Content, "内部错误") {
		t.Errorf("panic should be reported structurally, got %q", res.Content)
	}
}

// TestRegistryExecuteCancelVsTimeout：外部取消与超时的错误文案要能区分（R9）。
func TestRegistryExecuteCancelVsTimeout(t *testing.T) {
	r := NewRegistry(t.TempDir())
	r.Register(fakeTool{name: "slow", fn: func(ctx context.Context, _ map[string]any) Result {
		<-ctx.Done()
		return fail("cancelled")
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := r.Execute(ctx, "slow", nil)
	if res.OK {
		t.Fatal("cancelled tool should fail")
	}
	if !strings.Contains(res.Content, "取消") {
		t.Errorf("expected cancel wording, got %q", res.Content)
	}
}

// TestBuiltinToolsReadOnly：六个内置工具的只读分类（R26）。
func TestBuiltinToolsReadOnly(t *testing.T) {
	r := NewRegistry(t.TempDir())
	RegisterAll(r, t.TempDir())
	want := map[string]bool{
		"read_file":   true,
		"find_files":  true,
		"search_code": true,
		"write_file":  false,
		"edit_file":   false,
		"run_command": false,
	}
	for name, wantRO := range want {
		tool, found := r.Get(name)
		if !found {
			t.Fatalf("tool %s not registered", name)
		}
		if got := tool.IsReadOnly(); got != wantRO {
			t.Errorf("%s.IsReadOnly() = %v, want %v", name, got, wantRO)
		}
	}
}

func TestRegistryExecuteTimeout(t *testing.T) {
	r := NewRegistry(t.TempDir())
	r.Register(fakeTool{name: "slow", fn: func(ctx context.Context, _ map[string]any) Result {
		select {
		case <-time.After(2 * time.Second):
			return ok("done")
		case <-ctx.Done():
			return fail("cancelled")
		}
	}})
	// defaultTimeout 是 30s，这里改不了常量，改用 ctx 取消验证传播即可。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res := r.Execute(ctx, "slow", nil)
	if res.OK {
		t.Errorf("expected failure on ctx cancel, got %+v", res)
	}
}
