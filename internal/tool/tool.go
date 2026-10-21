// Package tool 定义 NekoCode 的工具系统：统一 Tool 接口、注册中心、
// 结构化执行结果与限制在工作目录内的路径守卫。
package tool

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// defaultTimeout 是单个工具执行的默认超时。
const defaultTimeout = 30 * time.Second

// Result 是工具执行的结构化结果。失败时 OK=false，Content 为错误说明，回灌给模型。
type Result struct {
	OK      bool
	Content string
}

// 构造函数，构造一个成功的 Result 或失败的 Result。
func ok(content string) Result { return Result{OK: true, Content: content} }
func fail(format string, a ...any) Result {
	return Result{OK: false, Content: fmt.Sprintf(format, a...)}
}

// Tool 是所有工具实现的统一接口。
type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any // JSON Schema：{"type":"object","properties":{...},"required":[...]}
	NeedsConfirm() bool     // 有副作用的工具（write/edit/bash）返回 true
	IsReadOnly() bool       // 只读工具返回 true，供 agent 分组并发执行（R26）
	Execute(ctx context.Context, args map[string]any) Result
}

// Definition 是工具的协议无关定义，供 provider 转成各家的 tools 列表。
type Definition struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Registry 集中登记工具，按名查找，并给出定义列表。
type Registry struct {
	root  string
	tools map[string]Tool
}

// NewRegistry 创建以 root 为工作目录根的注册中心。
func NewRegistry(root string) *Registry {
	return &Registry{root: root, tools: map[string]Tool{}}
}

// Register 登记一个工具。
func (r *Registry) Register(t Tool) { r.tools[t.Name()] = t }

// Get 按名查找工具。
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions 返回全部工具定义（按名排序，稳定顺序）。
func (r *Registry) Definitions() []Definition {
	defs := make([]Definition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, Definition{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// Execute 按名执行工具，套用默认超时；工具不存在返回结构化错误。
// 工具内部的 panic 被 recover 成结构化错误（R12），不会炸掉进程。
func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) Result {
	t, found := r.Get(name)
	if !found {
		return fail("未知工具: %s", name)
	}
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// done 带缓冲，超时后执行 goroutine 仍能发送并退出，不会泄漏。
	done := make(chan Result, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				done <- fail("工具 %s 内部错误: %v", name, rec)
			}
		}()
		done <- t.Execute(cctx, args)
	}()
	select {
	case res := <-done:
		return res
	case <-cctx.Done():
		// 区分「外部取消」与「超时」：前者是用户打断（R9），后者是工具跑太久。
		if ctx.Err() != nil {
			return fail("工具 %s 被取消", name)
		}
		return fail("工具 %s 执行超时（%s）", name, defaultTimeout)
	}
}
