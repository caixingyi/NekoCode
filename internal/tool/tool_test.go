package tool

import (
	"context"
	"testing"
	"time"
)

type fakeTool struct {
	name    string
	confirm bool
	fn      func(context.Context, map[string]any) Result
}

func (f fakeTool) Name() string           { return f.name }
func (f fakeTool) Description() string    { return "fake " + f.name }
func (f fakeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (f fakeTool) NeedsConfirm() bool     { return f.confirm }
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
