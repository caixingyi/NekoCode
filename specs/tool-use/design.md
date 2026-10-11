# 工具系统（Tool Use）技术设计

状态：已确认

## 总体方案

在 step 1 四层之上，新增 `internal/tool` 层，并扩展 message / provider / tui：

1. **`internal/tool`**：`Tool` 接口、`Registry` 注册中心、六个工具实现、路径守卫（限 cwd）、结构化结果 `Result`。
2. **`internal/message`**：`Message` 扩展 `ToolCalls`（tool_use）与 `ToolResults`（tool_result）；`Normalize` 增加 tools 定义与两家的工具块归一化。
3. **`internal/provider`**：`Stream` 增加 tools 参数；SSE 解析新增工具调用事件（拼接分片 JSON）；`StreamEvent` 增加工具调用类型。
4. **`internal/tui`**：编排单轮流程（首调 → 执行工具 → 回灌 → 再调）、确认交互（y/n/a）、工具调用与结果渲染。

数据流：用户输入 → Stream(含 tools) → 收到 tool_use → TUI 逐个确认(有副作用的) → tool.Execute → tool_result 写回会话 → 再次 Stream → 最终文本 → 停。

## 需求对应关系

- R1/R2：`tool.Tool` 接口 + `tool.Registry`（`Register`/`Get`/`Definitions()`）。
- R3：`Execute(ctx, args) Result`，Registry 层加超时；`Result{OK, Content, Error}` 结构化。
- R4-R9：六个工具各实现 `Tool`，见下。
- R10-R12：确认在 tui 层（工具声明 `NeedsConfirm() bool`）；拒绝返回 `Result{OK:false, Content:"用户已拒绝"}`。
- R13：`tool.resolvePath` 统一把路径解析进 cwd（含 `EvalSymlinks` 软链逃逸防护），越界返回错误。
- R14：`message.Message` 加 `ToolCalls []ToolCall` / `ToolResults []ToolResult`；`Normalize` 归一化。
- R15：`provider.Stream(ctx, system, tools, msgs)`；SSE 拼接工具参数分片。
- R16/R17：tui 编排两次调用；一次回复的多个 tool_use 顺序执行、结果一起回灌。
- R18：ToolCall/ToolResult 随 Message 落盘，`/resume` 恢复。
- R19：`internal/tui/markdown.go`，用 glamour 渲染已完成消息；流式阶段仍走纯文本分支。
- R20：`view.go` 的 `renderMessage` 对 ToolCalls/ToolResults 输出 `🔧`/`✓`/`✗` 一行摘要（`oneLine`/`argsSummary` 截断）。

## 技术决策

- 决策 1：**工具层独立包 `internal/tool`，不依赖 provider/tui**。理由：工具是纯粹的「输入 args → 输出 Result」，与 LLM 协议、UI 解耦，好测试、好扩展。tui 负责编排与确认，provider 负责协议。
- 决策 2：**参数 Schema 手写 JSON（`map[string]any`）**，不做 struct tag 反射生成。理由：六个工具 Schema 简单，手写直观可控；反射生成是过度工程。
- 决策 3：**确认放 tui 层，不放 tool 层**。理由：确认是 UI 交互（阻塞等待用户按键），tool 层保持纯函数无副作用于流程；工具只声明 `NeedsConfirm()`。
- 决策 4：**edit 用原文唯一匹配替换**（strings.Count==1 才替换）。理由：spec R6；比整文件重写省 token、精确、可预览 diff。
- 决策 5：**tool_result 作为 user 消息回灌**（Anthropic：user 消息含 tool_result blocks；OpenAI：role=tool 消息）。理由：符合两家协议。
- 决策 6：**助手回复用 glamour 渲染 Markdown，且只在消息完成后渲染**。理由：工具结果、代码片段用 Markdown 呈现可读性显著更好；流式阶段文本是半截的 Markdown（代码块未闭合），实时渲染会出现闪烁与错乱，故流式期间保持纯文本、结束后整体渲染一次。渲染器按折行宽度缓存，宽度变化时重建。未采用方案：自研 Markdown 渲染（成本高、效果差）；流式实时渲染（抖动、性能开销）。
- 决策 7：**工具调用/结果在会话里只显示一行摘要**。理由：MCP/Agent 类工具输出动辄数百行，全量渲染会淹没对话；完整结果已落盘并回灌给模型，界面只需让用户知道「调了什么、成没成」。

## 影响范围

```
新增:
  internal/tool/
    tool.go          # Tool 接口、Result、Definition、Registry、执行超时
    path.go          # argString、resolvePath 路径守卫(限 cwd + 软链)
    file.go          # read_file / write_file / edit_file
    exec.go          # run_command
    search.go        # find_files / search_code
    register.go      # RegisterAll 注册六工具
    *_test.go
  internal/message/normalize.go # 两家请求体与工具块归一化（从 message.go 拆出）
  internal/provider/tools.go    # parseToolCall：拼接后的参数 JSON → ToolCall
  internal/tui/tools_flow.go    # 单轮编排 + 确认状态机
  internal/tui/markdown.go      # glamour 渲染助手回复（R19）
修改:
  internal/message/message.go   # Message 加 ToolCalls/ToolResults；ToolCall/ToolResult 类型
  internal/provider/provider.go # Stream 加 tools 参数；StreamEvent 加 EventToolCall/ToolCall
  internal/provider/anthropic.go# 请求带 tools；解析 tool_use / input_json_delta
  internal/provider/openai.go   # 请求带 tools；解析 tool_calls 分片
  internal/tui/{model,update,stream,view}.go # 编排/确认/渲染
  internal/tui/view.go          # 另含装饰性调整：欢迎页猫脸改为 (・ω・)/つ つ，提示与确认样式
  go.mod / go.sum               # 新增 github.com/charmbracelet/glamour（及间接依赖）
  cmd/nekocode/main.go          # 注入 registry(cwd)、tools 传入 Stream
```

## 数据模型 / 接口变化

```go
// internal/tool
type Result struct { OK bool; Content string }   // 失败时 OK=false，Content 为错误说明
type Tool interface {
    Name() string
    Description() string
    Schema() map[string]any        // JSON Schema (properties/required)
    NeedsConfirm() bool            // write/edit/bash = true
    Execute(ctx context.Context, args map[string]any) Result
}
type Registry struct{ /* root string; tools map[string]Tool */ }
func (r *Registry) Definitions() []Definition   // 供 provider 转协议

// internal/message
type ToolCall   struct { ID, Name string; Input map[string]any }
type ToolResult struct { ID, Content string; IsError bool }
// Message 增加:
//   ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`   // assistant 发起
//   ToolResults []ToolResult `json:"tool_results,omitempty"` // user 回灌

// internal/provider
// Stream 签名: Stream(ctx, system string, tools []tool.Definition, msgs []message.Message) <-chan StreamEvent
// StreamEvent 增加: EventToolCall（携带完整 ToolCall，参数拼接完成后发出）

// internal/tui
// New(cfg, profileName string, store *session.Store, registry *tool.Registry)   // registry 可为 nil（禁用工具）
// renderMessage(msg message.Message, width int) string                          // 加 width 供 Markdown 折行
// Model.busy() bool   // streaming || confirming != nil || toolExecuting，为真时输入框隐藏且不吃按键
```

## 边界情况

- edit old 匹配 0 次 / 多次 → Result{OK:false} 附计数，模型重试。
- 路径越界（`../`、绝对路径逃逸、cwd 内软链接指向外部）→ Result{OK:false}。
- run_command 超时(30s) / 非零退出 → Result 含退出码与截断输出，OK 视为成功执行（结果里体现失败）。
- 大输出（>30KB/200 行）/ 大文件（>2000 行）→ 截断并标注「已截断」。
- 用户拒绝确认 → Result{OK:false, "用户已拒绝执行"}。
- 第 2 次回复又含 tool_use → 忽略、不执行（R16）；界面提示「已达单轮上限」。
- 流式工具参数 JSON 分片未拼完就断流 → 丢弃该调用并报错。
- 会话恢复：tool_use/tool_result 需与 assistant/user 消息严格配对，否则 Anthropic 会拒。
- Markdown 渲染失败（glamour 报错）→ 回退为原文，不中断渲染。
- 终端过窄（width-4 < 20）→ 折行宽度下限取 20，避免 glamour 极端折行。
- 工具参数为空/缺 key（`argsSummary`）→ 只显示工具名，不显示参数。
- 工具完整输出很长 → 会话只显示一行摘要（R20），完整内容仍在 Message 中落盘。

## 风险

- **两家工具协议差异大**：Anthropic tool_use blocks + tool_result；OpenAI tool_calls + role=tool。归一化需仔细，尤其流式分片拼接 → 用 httptest 覆盖两家。
- **消息配对严格**：Anthropic 要求 tool_use 后紧跟含对应 tool_result 的 user 消息 → Normalize 保证顺序与 id 对应。
- **确认阻塞与 MVU**：确认是异步交互，需在 bubbletea 里用「待确认」状态挂起、按键推进 → 设计确认状态机。
- **路径守卫**：resolvePath 用 filepath.Abs + EvalSymlinks（root 与目标路径的已存在部分）做软链规范化，再前缀校验，拦截 cwd 内软链指向外部的逃逸；对尚不存在的写路径只解析已存在祖先。
- **新增依赖**：glamour 引入 chroma/goldmark/bluemonday 等一串间接依赖，增加构建体积与供应链面。缓解：仅用于已完成的助手消息，失败即回退原文。
- **ANSI 输出影响**：渲染后的助手消息含 ANSI 转义，终端内复制会带控制符；宽度计算需基于渲染后文本。

## 测试策略

- 单元：每个工具（正常/边界/越界/截断）；resolvePath 守卫；Registry 注册与 Definitions；edit 匹配 0/1/多。
- 单元：message.Normalize 两家工具块归一化（tool_use/tool_result 顺序与配对）。
- 集成：httptest 模拟两家「首次返回 tool_use → 回灌 → 再返回文本」全流程；多工具并行。
- 手动：真实 key 下读/改/跑确认流程、拒绝、越界、错误恢复、/resume。
- 单元：`renderMessage` 传 width（tui_test.go 已覆盖）；`renderMarkdown` 目前仅经助手分支间接覆盖，**无直接单测**，Markdown 观感依赖手动验证。
- 手动：不同终端宽度下代码块/列表折行效果；窄终端（<24 列）不崩。
