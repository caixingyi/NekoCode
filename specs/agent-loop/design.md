# Agent Loop（ReAct 循环）技术设计

状态：已完成

## 总体方案

把 step 2 散在 `internal/tui/tools_flow.go` 里的单轮编排，提升为一个**无 UI 依赖、无 IO 依赖的纯循环包** `internal/agent`：调用方传入历史与输入，它返回一条事件流；TUI 退化为事件消费者，只负责渲染、转发确认、落盘。

```
internal/agent/
  agent.go     # Config、Agent、Run(ctx, history, input) <-chan Event
  event.go     # Event / EventType / StopReason / Usage
  loop.go      # 单轮：Stream → 收集 → 判定 → 执行 → 原子提交 → 下一轮
  batch.go     # 读/写分类、并发调度、按序回填、确认与拒绝
  guard.go     # 轮数上限、重复调用检测、上下文超限检查
  plan.go      # plan 模式 pre 拦截
  confirm.go   # Confirmer 接口与 Decision
```

关键取向：**agent 无状态**。它不持有会话、不写文件、不认识 bubbletea。每次 `Run` 传入完整历史，每个轮次事件回带完整历史快照，由 TUI 决定何时落盘。

数据流：

```
用户输入
  └─> tui: agent.Run(ctx, sess.Messages, input)
        └─ agent goroutine ──> <-chan Event ──> tui: 渲染 / 确认 / 落盘
                                   │
                                   └─ Confirmer.Confirm(ctx, call) → tui 弹确认 → Decision
```

## 需求对应关系

- R1：`loop.go` 的 `runTurn` 循环；每轮把上一轮的 tool_result 留在历史里再发下一次请求。
- R2/R3：`event.go` 的 `Event` 结构与事件类型；元信息字段 `Turn` / `Batch` / `Duration` / `Usage` / `StopReason`。
- R4：`internal/agent` 只依赖 `provider` / `message` / `tool` 三个包，不 import `tui`；测试注入 fake provider 与 fake tool。
- R5：一轮结束后按「无 tool_use / stop_reason=end_turn / 空响应」判定 `StopEndTurn` / `StopEmptyResponse`。
- R6：`guard.go` 的轮数计数，超过 `MaxTurns`（默认 25）→ `StopMaxTurns`。
- R7：`stop_reason=max_tokens` → `StopMaxTokens`（见 R24 的 provider 扩展）。
- R8：`guard.go` 的 `repeatDetector`，连续 3 次相同调用 → `StopRepeatCall`。
- R9：`ctx` 贯穿 Stream、批执行与确认；取消时走「补全结果 → 原子提交」路径（见「技术决策 4」）。
- R10/R11：`batch.go` 的分类与两段式调度（并发读 → 屏障 → 串行写），结果按原始下标写入数组。
- R12：`tool.Registry.Execute` 的 goroutine 加 `recover`（当前实现没有，panic 会炸掉进程）。
- R13：写批串行内逐个 `Confirmer.Confirm`；拒绝后给本批剩余写类合成「用户拒绝」结果并跳出写批。
- R14/R17：`plan.go` + `session.Session.PlanMode` 持久化 + `/plan` 命令。
- R15/R16：`plan.go` 的 `preCheck` 拦截写类，合成结构化结果；`Registry.Definitions()` 不做裁剪。
- R18/R19：`ctx` 取消贯穿；确认态 Esc 由 TUI 映射为 `DecisionDeny`，不 cancel ctx。
- R20：不改工具级超时；不引入循环级超时。
- R21：`guard.go` 注入式 `ContextLimit` 检查（默认由 TUI 注入 `aictx.ShouldSummarize`）。
- R22：见「技术决策 2」的**原子提交单元**——落盘点永远落在 tool_use/tool_result 配对的边界上。
- R23：TUI 把每轮 assistant 消息（含中间轮）按同一套 `renderMessage` 渲染。
- R24/R25：`provider.StreamEvent` 加 `StopReason` 与 `Usage`；两家 SSE 解析相应补齐。
- R26：`tool.Tool` 加 `IsReadOnly()`；`run_command` 返回 false。
- R27：`preCheck` 就是那个「拦截位」，目前只挂了 plan 规则。

## 技术决策

### 决策 1：`Run` 起自己的 goroutine，返回 `<-chan Event`

- 理由：与已有的 `provider.Stream` 完全同构，TUI 侧 `waitForEvent` 模式可直接复用；消费者只需在 channel 上读，用 `ctx` 取消。
- 未采用方案：`Run` 阻塞直到结束、事件走回调函数。回调会把「谁在哪个 goroutine 上执行」这件事藏起来，取消语义也更绕。

### 决策 2：agent 无状态 + 轮次事件回带完整历史快照（**关键**）

`Run(ctx, history, input) <-chan Event`，`EventTurnEnd` 携带 `Messages []message.Message`（本轮提交后的完整历史），TUI 直接 `sess.Messages = ev.Messages` 后落盘。

- 理由：agent 不碰文件系统，测试时不用 mock 存储；TUI 也不必再维护一份平行的消息状态（现在的 `sess.Messages` 与 `streamBuf/pendingCalls` 就是这种双份状态，容易不一致）。
- 收益还在于**落盘边界天然合法**：`EventTurnEnd` 只在「本轮 assistant 消息 + 其后的工具结果消息」都齐了之后才发，所以任何一次落盘都是自洽的历史，进程被杀也不会留下悬空的 tool_use。

### 决策 3：确认走接口，不走事件流

```go
type Confirmer interface {
    Confirm(ctx context.Context, call message.ToolCall, preview string) (Decision, error)
}
```

- 理由：确认是请求/应答语义。用单向事件流表达需要再加一条反向 channel，且「谁负责超时/取消」会变得含糊；接口签名直接、`ctx` 取消天然可用。
- TUI 侧实现（`tuiConfirmer`）：`Program.Send(confirmRequestMsg{...})` 把请求投给 bubbletea，再从 `reply chan Decision` 读结果。agent 在独立 goroutine 中阻塞，不会卡住 MVU 的 `Update`。
- **`Confirmer` 为 nil 时，写类工具一律按「拒绝」处理**（安全默认），而不是放行。

### 决策 4：取消 = 补全 + 原子提交，而不是直接丢弃

取消可以发生在三个位置，三处都必须让历史保持配对：

| 取消位置 | 处理 |
| --- | --- |
| 流式中（本轮还没收到 tool_use） | 保留已生成文本，作为本轮 assistant 消息提交 |
| 流式中（已收到部分 tool_use） | 保留文本 + 已收到的 tool_use，未执行的全部补「用户取消」结果 |
| 工具执行中 | 用 ctx 打断在跑的工具（其结果记为失败），排队未跑的全部补「用户取消」结果 |

- 理由：Anthropic 要求每个 tool_use 必须有配对的 tool_result，否则**下一次请求直接被 API 拒绝**。这条路径不补全，用户取消一次之后就再也发不出消息了。
- 未采用方案：取消即丢弃整个轮次。实现简单，但会连带丢掉已经跑完的读结果，且取消点之后的上下文全没了。

### 决策 5：读/写分类由工具自己声明

`Tool` 接口加 `IsReadOnly() bool`，而不是在 agent 里写 `switch name { case "read_file": ... }`。

- 理由：新增工具时不需要改 agent，分类信息与工具实现放在一起不会漂移。
- `run_command` 声明为非只读：它既能 `git status` 也能 `rm -rf`，静态无法区分，按最坏情况归类。

### 决策 6：并发调度用两段式，结果按下标写入

```go
func (a *Agent) runBatch(ctx context.Context, calls []message.ToolCall) ([]message.ToolResult, bool /*aborted*/)
```

1. 先扫一遍分类，记录读类的**原始下标**；写类保持原有相对顺序。
2. 读批：`chan struct{}` 做信号量（容量 `MaxParallel`，默认 8），`sync.WaitGroup` 等齐。每个 goroutine 把结果写进 `results[originalIndex]`——**各写各的下标，无需加锁，天然保序**，省掉一次排序。
3. 屏障：等读批全部结束。
4. 写批：按原始顺序串行；每个先过 `preCheck`（plan 拦截），再 `Confirm`，再 `Execute`。
5. 返回 `results`（已按原始顺序），未填充的位置补「已取消/已拒绝」结果后返回。

- 未采用方案 1：保持模型给出的交错顺序、只在相邻读类间并发。用户已明确选择「先读后写」的分组重排。
- 未采用方案 2：`errgroup`。需要额外引入 `golang.org/x/sync` 为直接依赖，且这里只需要一个信号量，标准库十行就够。

### 决策 7：plan 模式只拦执行，不裁工具定义

按用户选择：工具定义照常全量下发，只在 `preCheck` 拦截写类。

- 代价与缓解：模型可能先撞一次墙才知道自己在计划模式。拦截返回的结果文案必须足够硬（「当前处于计划模式，写操作已被阻止；请直接输出计划，不要重试同一写操作」），并且被拦的调用**计入重复检测**（连续 3 次被拦即中止），避免它在原地打转把轮数烧光。
- 未采用方案：只下发只读工具定义。更省 token，但用户认为这属于「替模型做决定」，且会让模型看不到「本来可以用什么工具」，规划质量下降。

### 决策 8：上下文检查用注入式回调，不在 agent 里 import context 包

`Config.ContextLimit func([]message.Message) bool`，TUI 注入 `func(msgs) { return aictx.ShouldSummarize(m.profile, msgs) }`。

- 理由：agent 保持对 `internal/context`（内含 Summarize 与真实 provider 调用）零依赖，测试时可以传一个「第 2 轮就返回 true」的假函数来验证 `StopContextLimit`。
- 检查时机：**每轮结束时**（而不是流式前），因为一轮结束后才知道这轮的工具体量。

### 决策 9：事件里不重复携带「最终回复」

用户列的「最终回复」事件，映射为 `EventTurnEnd`（最后一轮）+ `EventDone`：文本本身已经通过 `EventTextDelta` 逐字给出、由 `EventTurnEnd` 落定，再单独发一个带完整文本的 Final 事件是冗余的。

- 未采用方案：额外 `EventFinal{Text string}`。消费者要在两处处理同一份文本，容易写出重复渲染的 bug。

## 影响范围

```
新增:
  internal/agent/
    agent.go        # Config、Agent、Run
    event.go        # Event、EventType、StopReason、Usage
    loop.go         # runTurn：流式收集、终止判定、原子提交
    batch.go        # 分类、并发读、串行写、按序回填、确认与拒绝
    guard.go        # 轮数、重复调用、上下文超限
    plan.go         # plan 模式拦截（preCheck）
    confirm.go      # Confirmer、Decision
    *_test.go       # fake provider + fake tool 的确定性测试
  internal/tui/
    agent_flow.go   # 消费 agent 事件、路由到渲染/确认/落盘
    confirmer.go    # tuiConfirmer：Program.Send + reply channel
删除/重写:
  internal/tui/tools_flow.go   # 单轮编排整体迁走；确认预览等纯函数保留
修改:
  internal/tool/tool.go        # Tool 接口加 IsReadOnly()；Execute 的 goroutine 加 recover
  internal/tool/{file,exec,search}.go  # 各工具实现 IsReadOnly()
  internal/provider/provider.go # StreamEvent 加 StopReason / Usage；统一停止原因常量与归一化函数
  internal/provider/anthropic.go# 解析 message_start/message_delta 的 usage 与 stop_reason
  internal/provider/openai.go   # 请求带 stream_options.include_usage；解析 finish_reason 与 usage
  internal/message/normalize.go # openaiRequest 加 stream_options
  internal/config/config.go     # Profile 加 stream_usage（默认开）+ EffectiveStreamUsage()
  internal/message/message.go   # （如需）Usage 的落地结构；确认无须扩展 Message
  internal/session/session.go   # Session 加 PlanMode
  internal/tui/{model,update,view,stream}.go # 编排字段替换、状态栏、plan 标识
  internal/tui/commands.go      # 新增 /plan；/help 文案
  cmd/nekocode/main.go          # 无需改动（TUI 内部构造 agent）
```

## 数据模型 / 接口变化

```go
// internal/agent —— 全部为新增
type StopReason string
const (
    StopEndTurn      StopReason = "end_turn"        // 模型显式结束或无工具调用
    StopEmptyResponse StopReason = "empty_response" // 无文本、无工具调用
    StopMaxTokens    StopReason = "max_tokens"      // 输出被截断
    StopMaxTurns     StopReason = "max_turns"
    StopRepeatCall   StopReason = "repeat_call"
    StopContextLimit StopReason = "context_limit"
    StopCanceled     StopReason = "canceled"
    StopError        StopReason = "error"
)

type EventType int
const (
    EventUserMessage    EventType = iota // 循环开始，回显本轮输入
    EventThinkingDelta                   // Text 为增量；Signature 非空表示 thinking 块结束
    EventTextDelta
    EventToolCallStart                   // ToolCall + Batch + NeedsConfirm
    EventToolResult                      // Result + Duration + Batch
    EventToolRejected                    // 用户拒绝了该写类工具（TUI 据此提示本批剩余写类已跳过）
    EventToolBlocked                     // plan 模式下写类工具被拦截
    EventTurnEnd                         // Messages 快照 + StopReason(本轮) + Usage(本轮)
    EventDone                            // StopReason(整体) + Usage(累计) + Messages 快照
    EventError                           // Err + Messages 快照
)

type Event struct {
    Type      EventType
    Turn      int             // 轮次序号，从 1 开始；EventUserMessage 为 0
    Batch     int             // 轮内批次号：同一批工具共享一个号
    Text      string
    Signature string
    ToolCall  message.ToolCall
    Result    message.ToolResult
    NeedsConfirm bool
    Duration  time.Duration
    Usage     provider.Usage  // EventTurnEnd=本轮，EventDone=累计
    StopReason StopReason
    Messages  []message.Message // 提交后的完整历史快照
    Err       error
}

// 复用 provider.Usage，避免在两个包之间来回转换。
type Decision int
const (DecisionDeny Decision = iota; DecisionAllow; DecisionAllowAll)

type Confirmer interface {
    Confirm(ctx context.Context, call message.ToolCall, preview string) (Decision, error)
}
// Describe(call) 生成传给 Confirm 的预览文本（沿用 step 2 呈现方式）。

type Config struct {
    Provider     provider.Provider
    Registry     *tool.Registry
    SystemPrompt string
    PlanMode     bool
    MaxTurns     int  // <=0 → 25
    MaxParallel  int  // <=0 → 8
    RepeatLimit  int  // <2  → 3
    Confirmer    Confirmer
    ContextLimit func([]message.Message) bool
}

func New(cfg Config) *Agent
func (a *Agent) Run(ctx context.Context, history []message.Message, input string) <-chan Event

// internal/tool —— 接口扩展
type Tool interface {
    Name() string
    Description() string
    Schema() map[string]any
    NeedsConfirm() bool
    IsReadOnly() bool   // 新增：供 agent 分组；read/find/search=true，write/edit/run=false
    Execute(ctx context.Context, args map[string]any) Result
}

// internal/provider —— 事件扩展
type StreamEvent struct {
    Type       EventType
    Text       string
    Err        error
    Signature  string
    ToolCall   message.ToolCall
    StopReason string   // 新增：Anthropic stop_reason / OpenAI finish_reason
    Usage      *Usage   // 新增：本轮用量（可能为 nil，服务端没回）
}

// internal/session —— 状态扩展
type Session struct {
    // ...既有字段
    PlanMode bool `json:"plan_mode,omitempty"`
}
```

**确认预览文案**沿用 step 2 的 `confirmPreview`（run 显示命令、write 显示内容预览、edit 显示 old/new），由 TUI 侧在实现 `Confirmer` 时生成后传给 `Confirm`。

## 边界情况

- **取消点在半批次**：并发读跑完 3 个、第 4 个被 ctx 打断 → 已完成的保留真实结果，被中断的记为失败，未调度的补「用户取消」，全部回填后原子提交。
- **流式出错（网络断开/API 报错）且已收到 tool_use**：同样必须补结果再提交，否则历史非法；错误文本用「因上游错误未执行」。
- **一轮里模型请求了同一个工具 3 次**（参数相同）：直接触发重复检测，在**执行前**就中止，不白跑工具。
- **写批中第一个工具被拒绝**：剩余写类全部跳过并各补一条「用户拒绝」结果；**读批不受影响**（读批在写批之前已经跑完）。
- **plan 模式下模型只调写类、不输出计划**：每次拦截都计入重复检测，连续 3 次 → `StopRepeatCall` 中止，不会把 25 轮烧光。
- **plan 模式下模型同时请求读类与写类**：读类照常并发执行（计划确实需要读），写类被拦。
- **工具 panic**：`Registry.Execute` 的 goroutine 内 recover，转成 `Result{OK:false, Content:"工具内部错误: ..."}`，不能炸掉进程。（`done` channel 本就带缓冲，超时后执行 goroutine 仍能发送并退出，无需额外处理泄漏。）超时时区分「外部取消」与「超时」两种文案，前者对应 R9 的用户打断。
- **空响应**：模型只回了 thinking 或什么都没回 → `StopEmptyResponse`，不报错；已经落盘的 assistant 消息若为空则不入库（避免历史里出现空消息）。
- **stop_reason 缺失**：部分 OpenAI 兼容服务不返回 usage 或 finish_reason → 字段为空时降级为「按有无 tool_use 判定」，`Usage` 计为零，不阻塞循环。
- **串行写执行期间用户按 Ctrl+C**：正在跑的工具被 ctx 打断，剩下的补「用户取消」。
- **token 统计缺失导致上下文检查失效**：`ContextLimit` 由 TUI 注入 `aictx.Estimate` 估算（不依赖服务端 usage），所以服务端不回 usage 也不影响 R21。
- **`/resume` 恢复了一个 plan 模式下的会话**：开关随会话恢复，并立即在状态栏体现。
- **`/new` / `/switch`**：`/new` 回到普通模式（新会话默认 `PlanMode=false`）；`/switch` 不动 plan 状态（它是同一个会话）。

## 风险

- **provider 协议差异**：Anthropic 的 `stop_reason` 在 `message_delta`、input tokens 在 `message_start`、output tokens 需累加；OpenAI 需要显式 `stream_options.include_usage`，且**部分兼容服务（含 DeepSeek 一类自建网关）不支持该字段会直接 400**。缓解：profile 增加 `stream_usage` 可选配置（默认开启，可关）；解析失败一律降级为「用量未知」，绝不因为拿不到 usage 而中断对话。
- **OpenAI 兼容服务的 finish_reason 取值不一**（`stop` / `length` / `tool_calls` / 空）：映射时以「有 tool_use 就继续」为主判据，`length` 才当作截断，未知值一律当 `stop` 处理。缓解：单测覆盖各取值 + 未知值。
- **确认的 goroutine 桥接**：`Confirmer.Confirm` 在 agent goroutine 上阻塞，靠 `Program.Send` 回投 UI。若 TUI 在等待期间退出（`tea.Quit`），`reply` channel 必须被关闭或 `ctx` 被取消，否则 agent goroutine 永久阻塞。缓解：TUI 退出路径统一 `cancel()`，`Confirm` 内 `select { case <-reply: case <-ctx.Done(): }`。
- **历史快照的复制开销**：每轮 `EventTurnEnd` 复制整个历史切片。长会话（数千条消息）时是 O(n) 的结构体拷贝。缓解：消息体为字符串（共享底层数组），实测开销可忽略；若成为热点再改增量事件。
- **原子提交带来的语义变化**：中间轮的 assistant 消息不再「流完即入库」，而是和工具结果一起入库。这意味着流式过程中若 UI 崩溃，那些文本不会出现在 `/resume` 里（但也不会留下非法历史）。缓解：这是刻意取舍——宁可丢一次未完成的输出，也不留下会让 API 拒绝的历史。
- **重复检测误杀**：模型「写完再读一遍确认」是合法的，所以阈值定为连续 3 次而非 2 次。缓解：单测覆盖「读-写-读」序列不被触发。
- **从 TUI 抽走编排是较大改动**：`tools_flow.go` 与 `stream.go` 的现有行为（含 `inSecondStream`、`finalizeStream`）都要在 agent 侧重新表达，回归风险集中在确认流程与 `/resume` 的历史形状上。缓解：保留 step 2 的集成测试（两家 httptest 端到端、确认状态机）并改造成针对 agent 包。

## 实现记录与设计偏差

实现过程中对初稿做了以下修正，均已落到代码：

1. **main.go 需要一处接线**（初稿写的是「无需改动」）。确认请求要投回 UI 事件循环，
   必须拿到 `*tea.Program`：新增 `Model.SetProgram(p)`，main 在 `tea.NewProgram` 之后、
   `Run` 之前调用。为避免在 Model 里硬依赖 `*tea.Program`（测试无法构造），抽了一个
   只有 `Send(tea.Msg)` 的最小接口 `sender`，生产实现是 `programSender`，测试用
   `chanSender` 接进自建的事件循环驱动器。
2. **「本会话全部允许」需要 TUI 侧再持有一份**。agent 内部的 `allowAll` 随单次 `Run`
   结束而消失，而 R13 要求它跨消息生效。做法：Model 用 `atomic.Bool` 保存会话级开关，
   确认器短路时直接返回 `DecisionAllowAll`（不再打扰用户），并在用户按 `a` 时置位。
   用 atomic 是因为它同时被 UI goroutine 与 agent goroutine 访问。
3. **取消判定要早于上游错误判定**。取消会让正在进行的 HTTP 请求以 `context canceled`
   失败，若先判错误就会把用户的取消报成「上游请求出错」。
4. **取消键位**（R18、R19 的落地口径）：循环中 `Esc` 与 `Ctrl+C` 都取消循环；确认态下
   `Esc` 只拒绝当前工具、`Ctrl+C` 仍然取消整个循环；空闲时 `Esc` 无操作、`Ctrl+C` 退出程序。
5. **事件类型补了两个**：`EventToolRejected`（用户拒绝，用于提示「本批剩余写类已跳过」）
   与 `EventToolBlocked`（plan 模式拦截），否则 TUI 无法区分这两种都是 `IsError` 的结果。
6. **`Usage` 直接复用 `provider.Usage`**，不再在 agent 里定义同构结构体来回转换。
7. **`Describe`（确认预览）从 tui 移到 agent**：接口签名里带 `preview` 参数，预览就该由
   接口所在的一侧生成，否则每个消费方都要重复实现一遍；原 `tools_flow.go` 随之删除。
8. **`RepeatLimit < 2` 会被夹回默认 3**：阈值 1 意味着任何一次工具调用都算「重复」。

## 测试策略

- **单元（agent，用 fake provider + fake tool，无网络）**：
  - 单轮无工具 → `StopEndTurn`；空响应 → `StopEmptyResponse`；`stop_reason=max_tokens` → `StopMaxTokens`。
  - 多轮：第 1 轮 tool_use → 第 2 轮 tool_use → 第 3 轮文本，断言轮次序号、事件顺序、每轮历史快照的配对性。
  - 分批：一轮 3 读 2 写 → 断言读类并发（用带屏障的 fake tool 验证重叠）、写类串行、结果按原始下标回填。
  - 拒绝：写批第 1 个拒绝 → 断言剩余写类被跳过并各有「用户拒绝」结果、读类不受影响、循环继续。
  - 取消：在流式中 / 在工具执行中 / 在等待确认中三个位置各自 cancel → 断言历史 100% 配对（tool_use 数量 == tool_result 数量）。
  - 收场：轮数上限、连续 3 次重复调用（含「读-写-读」不触发的反例）、上下文超限回调返回 true。
  - plan：写类被拦且读类放行；被拦调用计入重复检测。
  - 工具 panic → 结构化失败结果，循环存活。
- **单元（provider）**：两家 SSE 流断言 `StopReason` 与 `Usage`；OpenAI 未知 finish_reason、缺 usage、`stream_usage=false` 三种降级。
- **单元（tool）**：六个工具的 `IsReadOnly()` 取值；`Execute` 内 panic 被 recover。
- **集成（tui）**：沿用并改造 step 2 的 httptest 端到端（Anthropic + OpenAI 各跑一次「多轮工具 → 最终文本」）；确认状态机改为经由 `tuiConfirmer` 驱动；`/plan` 开关的持久化与 `/resume` 恢复。
- **手动**：真实 key 下跑一个多步任务（读 → 改 → 跑测试 → 再改）；观察轮次/耗时/token 显示；中途 Esc 取消后再发消息；plan 模式下让它出一个计划。
