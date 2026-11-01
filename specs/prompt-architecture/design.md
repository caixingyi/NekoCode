# 提示词与指令架构 技术设计

状态：已完成

## 总体方案

本章新增两个基础包，并改动消息、provider、agent、context、session、tui 六层：

- `internal/prompt`：暴露 `prompt.Prompt`，由 `profile.SystemPrompt` 构造，封装稳定指令模块注册、拼装、优先级（R1–R5）以及模式/环境补充消息文案；TUI 与 agent 只依赖这个对象。
- `internal/environment`：采集 cwd、OS、架构、shell、时间与 Git 状态（R6）。
- `internal/message`：给消息模型增加“系统补充消息”的结构化标记与构造器；Anthropic 归一化增加 system/tool 缓存断点（R4、R7）。
- `internal/provider`：解析并归一化 Anthropic/OpenAI 的缓存用量（R10）。
- `internal/agent`：启动时确保环境消息；按轮注入 plan 模式提示；保证补充消息不拆散工具配对（R6、R8）。
- `internal/context`：压缩时保留环境消息、把摘要改成补充消息、按“真实消息”计算保留轮数（R9）。
- `internal/session`：标题跳过补充消息（R11）。
- `internal/tui`：拼装稳定 prompt、模式切换追加提示、状态栏展示缓存、隐藏补充消息（R6–R11）。

整体数据流：

```text
profile.system_prompt
        │
        ▼
prompt.New(custom) ──► Prompt.System() 稳定文本 ──► message.Normalize ──► provider
                                                     ▲
会话历史（env supplement / mode supplement / user / assistant / tool）┘

agent.Run:
  clone history
  → ensure env supplement（需要时采集并替换）
  → append user input
  → 每轮：按需追加 mode supplement → 调模型 → 执行工具 → commitTurn
  → 每轮事件携带完整历史快照，TUI 落盘
```

## 需求对应关系

- R1：`internal/prompt` 的 `Prompt` 在构造时按优先级拼装 `Module` 注册表；新增模块只追加注册项。
- R2：安全模块固定最高优先级并带 `Locked` 标记；`Prompt.System()` 开头写权威声明；不做运行时去重。
- R3：`prompt.New(profile.SystemPrompt)` 在安全模块之后插入用户自定义模块；空串整体省略。配置字段仍留在 `config.Profile`，但拼装逻辑只在 prompt 包。
- R4：稳定模块、自定义 prompt、工具定义组成稳定前缀；Anthropic 在最后一个 tool 与 system block 上设置 `cache_control`；OpenAI 不写该字段；动态内容只追加。
- R5：工具使用模块写通用规则；`read_file`、`edit_file`、`search_code`、`find_files`、`run_command` 的描述写具体约束；用测试检查关键短语同时存在。
- R6：`internal/environment.Collect` 采集并在 agent 启动时生成 env supplement；TUI 在 `/resume`、`/clear`、`/new` 后设置 `RefreshEnv`。
- R7：`message.Message` 增加 `IsSupplement`、`SupplementKind`、`SupplementSeq`、`SupplementMode`、`SupplementCadence`；补充消息用 user 角色 + 特殊标签承载；TUI 隐藏、标题跳过、注入点受工具配对约束。
- R8：TUI 在 `/plan` 切换时立即追加完整进入/退出提示；agent 在第 1、6、11… 轮注入完整、其余轮注入精简；刚追加的进入提示计入第 1 轮。
- R9：`context.Summarize` 保留 env supplement，把摘要生成为 `summary` supplement，按真实消息数保留最近轮次，并在安全边界切分。
- R10：`provider.Usage` 增加缓存字段；Anthropic/OpenAI 解析后归一化；TUI 状态栏展示最近一轮。
- R11：session 读写补充消息；标题只取首条真实用户消息。
- R12：tasks.md 中列出 8 个人工场景与记录表；不引入自动评估。

## 技术决策

### 决策 1：模块内容用 Go 常量，不加载外部文件

- 选择：`internal/prompt` 暴露 `Prompt`；`prompt.New(custom)` 在构造时完成模块拼装并缓存稳定文本，`System()` 直接返回。模块内容在 `sections.go` 用 raw string 定义。
- 理由：把所有“系统提示词”的默认内容、拼装和优先级收口到 prompt 包；TUI/agent 不再各自拼装 system 文本。Go 常量确定性最好，测试可直接比较字节。
- 未采用：包级 `Assemble(custom)` 让调用方每次自行拼装。结构更散，TUI/context/session 容易拿到不同版本的 system 文本。
- 未采用：`go:embed` 的 Markdown 文件。可维护性略好，但增加文件清单与构建路径，且容易被误解为“可配置指令文件”。

### 决策 2：自定义 `profile.system_prompt` 是 P900 附加模块

- 选择：安全边界 P1000（锁定）→ 自定义 P900 → 身份 P800 → 行为 P700 → 工具 P600 → 代码规范 P500 → 任务模式 P400 → 输出风格 P300。自定义内容由 `prompt.New(profile.SystemPrompt)` 传入，不进入 config 的运行时逻辑。
- 理由：满足“高优先级在前”的权威声明，同时保留旧配置能力；安全边界始终第一；配置只承载数据，prompt 包承载拼装语义。
- 未采用：把自定义 prompt 放最后再声明优先。顺序与权威声明会矛盾，模型更容易受后文影响。

### 决策 3：补充消息是 user 角色 + 特殊标签 + 结构化字段

- 选择：`Content` 存模型可见的 `<nekocode_supplement ...>` 文本；`Message` 同时保存 `IsSupplement`、`SupplementKind`、`SupplementSeq`、`SupplementMode`、`SupplementCadence` 供内部逻辑使用。
- 理由：Anthropic 的会话消息不接受 `system` role，OpenAI 的会话内 system/developer 在兼容网关上行为不一致；user 角色最稳。
- 未采用：原生 system/developer 消息。语义更干净，但跨协议与网关兼容性风险高。

### 决策 4：env supplement 固定 seq=1，mode/summary seq 从 2 开始

- 选择：环境消息永远位于首位，`SupplementSeq=1`，刷新时内容更新但序号不变；模式/摘要使用 `NextSupplementSeq`，最小为 2。
- 理由：env 是位置固定的快照，不参与“最新模式/摘要为准”的冲突裁决；固定 1 可避免“先追加 plan supplement、后插入 env supplement”造成的序号倒挂。
- 未采用：所有 supplement 按追加时间递增。env 会插到队首，序号与位置矛盾。

### 决策 5：agent 负责 env 与轮次提示，TUI 负责显式模式切换提示

- 选择：`/plan` 切换时 TUI 立即追加完整进入/退出 mode supplement；agent 负责 env supplement 与 cadence supplement。agent 在第 1 轮检查“最后一条 mode supplement 是否仍是未被 assistant 覆盖的完整 plan 提示”，是则跳过重复注入。
- 理由：TUI 知道用户显式切换，能立即落盘；agent 知道轮次，能控制 cadence。检查历史而不是额外布尔状态，`/clear`、`/resume`、重开进程都不会失配。
- 未采用：agent 检测模式变化并注入所有 transition supplement。会让“立即追加”变成“下一次运行时追加”，且 `/plan` 后不发送消息时状态不落盘。

### 决策 6：摘要改成 `summary` supplement，不再折进 system

- 选择：`context.Summarize` 输出 `[env supplement, summary supplement, 最近真实消息...]`。
- 理由：摘要若拼进 system，压缩后稳定前缀必然失效；改成会话补充消息后，system/tools 的缓存断点不受压缩影响。

### 决策 7：Anthropic 只加两个稳定断点，不做移动消息断点

- 选择：最后一个 tool 上 `cache_control: {"type":"ephemeral"}`；system 单个 text block 上也设置同样字段。不设置 top-level `cache_control`，不在最后一条消息上设置移动断点。
- 理由：本章目标是稳定前缀可缓存；对话历史属于变化层，追加消息不会破坏已缓存的 tools+system。两个断点足够验证策略，且改动面小。
- 未采用：Anthropic top-level `cache_control` 自动标记最后一个可缓存块。它可能落到动态消息上，且语义不如显式断点清晰。
- 用户已确认：本章不做移动消息断点，留到后续单独评估。
- 用户已确认：本章不新增 `cache_control` 关闭开关；Anthropic 兼容网关若拒绝该字段，先按错误排查，后续再补 profile 开关。

### 决策 8：OpenAI 依赖自动前缀缓存，不写 Anthropic 字段

- 选择：请求体保持不变；只解析 `usage.prompt_tokens_details.cached_tokens`。
- 理由：OpenAI 的缓存是自动的，写入 `cache_control` 可能被兼容网关拒绝。
- 未采用：为 OpenAI 模拟显式缓存标记。协议不支持。

### 决策 9：缓存用量归一化为“总输入 + 读取 + 写入”

- 选择：`Usage.InputTokens` 统一表示本轮总输入 token。
  - Anthropic：`input_tokens + cache_creation_input_tokens + cache_read_input_tokens`。
  - OpenAI：`prompt_tokens`（已含缓存读取）。
  - `CacheReadTokens`：Anthropic 的 `cache_read_input_tokens`；OpenAI 的 `cached_tokens`。
  - `CacheCreationTokens`：Anthropic 的 `cache_creation_input_tokens`；OpenAI 恒为 0。
  - `CacheUsageKnown`：provider 是否真的返回了缓存字段；Anthropic 用指针检测字段存在，OpenAI 用 `prompt_tokens_details` 指针检测。
- 理由：状态栏只需要一个命中率公式 `CacheReadTokens / InputTokens`；区分 0 与未知。
- 未采用：保留 Anthropic 原始 `input_tokens` 再加 `TotalInputTokens`。会多一个字段，现有 UI 展示也会变成“非缓存输入”而误导。

### 决策 10：环境快照只在会话开始与恢复时采集

- 选择：`agent.Config.RefreshEnv` 为真或历史里没有 env supplement 时采集；TUI 初始、`/resume`、`/clear`、`/new` 后置真，`startRun` 传给 agent 后置假。
- 理由：会话内原地刷新会改掉消息前缀，破坏缓存；恢复时必须换掉旧 cwd/分支。
- 未采用：每轮采集。信息最新，但 token 与缓存成本不可接受。

### 决策 11：plan cadence 为 1、6、11…

- 选择：第 1 轮完整；第 6、11、16… 轮完整；其余精简。进入模式时 TUI 追加的完整提示可覆盖第 1 轮，不重复。
- 理由：长循环中每 5 轮提醒一次，兼顾遵从性与 token；首轮完整保证方向正确。
- 未采用：每轮完整（贵）、只在切换时提示（容易遗忘）。

### 决策 13：摘要按真实消息数保留，并在安全边界切分

- 选择：先摘出 env supplement；统计非补充的“真实消息”，保留最近 `KeepRecentRounds*2` 条；切分点若落在 `user` 的 `tool_result` 消息上，向前移动直到不拆散 `assistant.tool_use` 与 `user.tool_result`。
- 理由：plan 模式下每轮都有 supplement，按消息总数保留会让 supplement 挤掉真实对话；工具配对是 Anthropic 的硬协议。
- 未采用：继续按消息切片。会缩短真实上下文，并可能产生悬空 tool_result。

### 决策 14：状态栏只展示最近一轮缓存，未知用 `—`

- 选择：`m.turnUsage` 保存最近一轮 `EventTurnEnd` 的 Usage；`header()` 追加 `缓存 read/total pct%`，未知显示 `缓存 —`；首轮读取为 0、写入 >0 时显示 `缓存 0% (写 X)`。
- 理由：累计命中率会被首轮和压缩污染；最近一轮最能验证策略。
- 未采用：写入会话 JSON。用户明确只选状态栏。

## 影响范围

新增：

- `internal/prompt/prompt.go`：`Prompt`、`New`、`System`、模式/环境 supplement 方法、优先级常量。
- `internal/prompt/sections.go`：内置模块文案。
- `internal/prompt/supplements.go`：enter/exit/full/brief plan 文案、env supplement 文案。
- `internal/prompt/prompt_test.go`。
- `internal/environment/environment.go`：`Info`、`Collect`、Git 状态解析。
- `internal/environment/environment_test.go`。
- `internal/message/supplement.go`：补充消息字段辅助、构造器、标签渲染、seq、body 清洗。
- `internal/message/supplement_test.go`。

修改：

- `internal/message/message.go`：`Message` 增加 supplement 字段。
- `internal/message/normalize.go`：Anthropic system 改 block 数组、tool/system 缓存断点。
- `internal/message/message_test.go`。
- `internal/provider/provider.go`：`Usage` 增加缓存字段、`Add`、`CacheHitRate`。
- `internal/provider/anthropic.go`：解析 `cache_read_input_tokens` / `cache_creation_input_tokens`。
- `internal/provider/openai.go`：解析 `prompt_tokens_details.cached_tokens`。
- `internal/provider/*_test.go`、`usage_test.go`。
- `internal/agent/agent.go`：`Config` 用 `Prompt *prompt.Prompt` 替换 `SystemPrompt string`，并增加 `WorkDir`、`RefreshEnv`、`CollectEnv`；`New` 对 nil Prompt 兜底；`run` 增加准备历史。
- `internal/agent/loop.go`：启动时 ensure env；每轮注入 mode supplement；跳过被 transition supplement 覆盖的首轮；请求使用 `cfg.Prompt.System()`。
- `internal/agent/agent_test.go`。
- `internal/context/context.go`：`ShouldSummarize`/`Summarize` 接收稳定 system 文本；摘要输出 supplement；保留 env；真实消息计数；安全切分。
- `internal/context/context_test.go`。
- `internal/session/session.go`：标题跳过 supplement；空标题不提前写死。
- `internal/session/session_test.go`。
- `internal/tui/model.go`：持有 `instructions *prompt.Prompt`、`envNeedsRefresh`、`workDir()`；初始化时用 profile 构造，`/switch`、`/resume` 后重建。
- `internal/tui/agent_flow.go`：把 `instructions` 传给 agent；ContextLimit 用 `instructions.System()`。
- `internal/tui/commands.go`：`/plan` 用 `instructions.EnterPlanSupplement()` / `ExitPlanSupplement()` 追加 mode supplement；`/resume`、`/clear`、`/new` 置 `envNeedsRefresh`，`/switch`、`/resume` 重建 `instructions`。
- `internal/tui/stream.go`：压缩与估算使用 `instructions.System()`。
- `internal/tui/update.go`：`ShouldSummarize` 新签名；`startRun` 重置 `turnUsage`。
- `internal/tui/view.go`：隐藏 supplement；状态栏缓存段；token 格式化与宽度降级。
- `internal/tui/*_test.go`。
- `internal/tool/tool.go`：`Registry.Root()`。
- `internal/tool/tool_test.go`。
- `internal/config/config.go`：`SystemPrompt` 字段保留；更新注释与模板说明；`DefaultSystemPrompt`/`EffectiveSystemPrompt` 已删除（运行时只用 prompt 包）。
- `internal/config/config_test.go`（如注释/legacy 测试需要）。

不修改：

- `internal/tool/file.go`、`search.go`、`exec.go` 的执行逻辑；只改 `Description()` 文案（R5）。
- 工具确认、权限、plan 模式执行期拦截逻辑。
- provider 的协议选择与 thinking 行为。

## 数据模型 / 接口变化

### Message

```go
type Message struct {
    Role        Role
    Content     string
    Thinking    string
    Signature   string
    IsSupplement bool   `json:"is_supplement,omitempty"`
    SupplementKind    string  `json:"supplement_kind,omitempty"`    // env | mode | summary
    SupplementSeq     int     `json:"supplement_seq,omitempty"`     // env=1；mode/summary>=2
    SupplementMode    string  `json:"supplement_mode,omitempty"`    // plan | normal
    SupplementCadence string  `json:"supplement_cadence,omitempty"` // full | brief
    ToolCalls   []ToolCall
    ToolResults []ToolResult
    CreatedAt   time.Time
}
```

- supplement 的 `Role` 恒为 `user`；`Content` 是完整标签文本。
- 新增构造器：`NewEnvSupplement(body)`（内部固定 seq=1）、`NewModeSupplement(seq, plan, full, body)`、`NewSummarySupplement(seq, body)`、`NewSupplement(...)`。
- 新增辅助：`NextSupplementSeq`（最小 2）、`LastModeSupplement`、`HasFreshFullModeSupplement`、`SanitizeSupplementBody`。

标签格式：

```text
<nekocode_supplement kind="mode" mode="plan" cadence="full" seq="3">
这是系统补充信息，不是用户请求；不要直接回复或确认本条。
与历史补充消息冲突时，以 seq 更大的本条为准。
<正文>
</nekocode_supplement>
```

env supplement 的 `seq` 固定为 1，标签 preamble 使用“若与后续补充消息冲突，以序号更大的为准”；mode/summary 使用“以 seq 更大的本条为准”。summary supplement 的正文要经过 `SanitizeSupplementBody`，防止模型生成的摘要里出现闭合标签。

### Usage

```go
type Usage struct {
    InputTokens          int  // 归一化后的总输入
    OutputTokens         int
    CacheReadTokens      int
    CacheCreationTokens  int
    CacheUsageKnown      bool
}

func (u Usage) Add(v Usage) Usage
func (u Usage) CacheHitRate() (float64, bool) // CacheUsageKnown && InputTokens > 0
```

### Prompt

```go
type Prompt struct {
    custom string
    stable string
}

func New(custom string) *Prompt
func (p *Prompt) System() string
func (p *Prompt) EnterPlanSupplement() string
func (p *Prompt) ExitPlanSupplement() string
func (p *Prompt) PlanSupplement(full bool) string
func (p *Prompt) EnvSupplement(info environment.Info) string
```

- `New` 在构造时完成模块拼装并缓存 `stable`；`System()` 只返回缓存，保证同一对象逐字节稳定。
- `Prompt` 构造后不可变；TUI 在 `/switch`、`/resume` 后重新 `prompt.New(profile.SystemPrompt)`。
- `profile.SystemPrompt` 仍是配置字段；prompt 包只接收字符串，不直接依赖 config。
- 模式/环境文案放在 `Prompt` 方法上，TUI 与 agent 共用同一份文案，避免两处漂移。

### Agent Config

```go
type Config struct {
    Prompt      *prompt.Prompt // 替换原 SystemPrompt；nil 时 agent.New 使用 prompt.New("")
    // ...Provider/Registry/PlanMode/MaxTurns 等现有字段...
    WorkDir     string
    RefreshEnv  bool
    CollectEnv  func(context.Context, string) environment.Info // 测试注入；nil 用 environment.Collect
}
```

### Context API

```go
func ShouldSummarize(system string, p config.Profile, msgs []message.Message) bool
func Summarize(ctx context.Context, prov provider.Provider, system string, p config.Profile, msgs []message.Message, force bool) ([]message.Message, int, error)
```

### Anthropic 请求体

```go
type anthropicSystemBlock struct {
    Type         string                `json:"type"`
    Text         string                `json:"text"`
    CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}
type anthropicTool struct {
    // ...现有字段...
    CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}
```

- `System` 从 `string` 改为 `[]anthropicSystemBlock`；只有一个 block，带 `cache_control`。
- 最后一个 tool 带 `cache_control`；tool 为空时不设置。
- 不设置请求级 top-level `cache_control`。

### OpenAI 流式 usage

```go
Usage *struct {
    PromptTokens     int `json:"prompt_tokens"`
    CompletionTokens int `json:"completion_tokens"`
    PromptTokensDetails *struct {
        CachedTokens *int `json:"cached_tokens"`
    } `json:"prompt_tokens_details"`
} `json:"usage"`
```

- `CacheUsageKnown = PromptTokensDetails != nil && CachedTokens != nil`。
- `InputTokens = PromptTokens`；`CacheReadTokens = *CachedTokens`；`CacheCreationTokens = 0`。

### Session

- `Session` JSON 结构不新增字段；supplement 信息随 `Messages` 落盘。
- `DeriveTitle` 跳过补充消息；无真实用户消息时返回空串，`Save` 不提前写死标题；`/resume` 列表对空标题用创建时间兜底展示。

## 边界情况

- 会话在非 Git 目录、git 未安装、git status 超时：env supplement 标注“不在 Git 仓库”或“状态未知”，不阻塞运行。
- detached HEAD、尚无提交的仓库、路径在子目录：分支字段分别显示 detached HEAD、分支名或 `HEAD`，不影响后续字段。
- `git status` 输出包含 rename/delete/space 路径：只统计索引状态、工作区状态、`??`，不解析 diff。
- `/resume` 时旧 env supplement 不在首位：替换并移动到首位；旧重复 env supplement 一并清除。
- `/clear` 后 plan mode 仍为开：history 为空，agent 首轮注入完整 plan supplement。
- `/plan` 后不发送消息直接退出：mode supplement 已落盘；下次运行按最后状态决定是否重复，不会丢模式。
- `/plan` 切换后 history 为空：mode supplement 成为首条消息；随后 agent 在其前面插入 env supplement，env seq=1、mode seq>=2。
- plan 模式下每轮 supplement 挤占上下文：摘要按真实消息数保留，supplement 不参与 keep 计数。
- 摘要切分点落在 `tool_result` 消息：向前移动切分点，绝不留下悬空 tool_result；摘要 supplement 是 user 角色，后面接 assistant 或新的 user 请求都合法。
- 摘要正文包含 `</nekocode_supplement>`：清洗后再包裹，避免标签提前闭合。
- Anthropic 连续出现 user supplement + user 请求 + user tool_result：依赖 API 合并同角色消息；归一化测试覆盖 block 顺序。若兼容网关拒绝，风险记录在案，本章不做自动合并。
- 缓存前缀不足 provider 最小长度：状态栏显示 0%，不视为失败；首轮只有 cache creation 时显示写入量。
- provider 未返回缓存字段：`CacheUsageKnown=false`，状态栏 `—`，不能显示 0%。
- 状态栏窄终端：按优先级省略帮助提示、累计 token、缓存明细；至少保留命中率或 `—`。
- 自定义 `system_prompt` 为空：不产生空模块；包含动态内容：缓存可能失效，由用户负责。
- 自定义 prompt 试图覆盖安全边界：安全模块仍在最前且声明不可覆盖；实际权限仍由工具层兜底。
- 运行中进程崩溃：cadence supplement 可能已发给模型但未落盘；下次运行重新注入，不尝试恢复未提交状态。
- 旧会话没有 env supplement：首次运行即使 `RefreshEnv=false` 也补建并置首。
- 会话标题：只有 mode supplement 时保持空标题，首条真实用户消息出现后再生成。

## 风险

- 提示词文案质量是本章的主要风险；模块过长会增加每轮成本，过短则约束不足。需要用 R12 场景迭代。
- Anthropic 兼容网关可能拒绝 system block 数组或 `cache_control`；本章不新增关闭开关，失败时需后续按 `StreamUsage` 的模式补配置。
- 连续 user supplement 在 Anthropic 上依赖同角色合并；个别网关可能不兼容。
- `Usage.InputTokens` 语义从“非缓存输入”改为“总输入”，需要同步更新所有 usage 测试与状态栏，避免旧断言误判。
- plan supplement 每轮注入会增加历史体积；cadence 与摘要 keep 规则必须配合，否则长循环更快触发压缩。
- env 的 `git status` 在大仓库可能接近 500ms 超时；超时降级会让模型看不到脏状态，需要接受。
- 工具描述与全局模块双重强化可能随时间漂移；用关键短语测试 + 人工评审降低风险。
- 不做运行时冲突裁决，模型仍可能看到矛盾规则；这是 spec 明确接受的取舍。
- 状态栏新增字段可能挤压窄终端；必须有宽度降级测试。
- `agent.Config.Prompt` 替换 `SystemPrompt` 是 API 变更；所有调用方与测试需同步，`agent.New` 对 nil 做兜底。
- prompt 包同时承载稳定文本与动态文案，边界必须守住：稳定模块不得引用环境、模式或会话状态，否则缓存前缀会随运行变化。

## 测试策略

### 单元测试

- `internal/prompt`：
  - 模块顺序、优先级、稳定性（同一 `Prompt` 的 `System()` 多次调用完全相等；两个相同 custom 的 `New` 结果也相等）。
  - 安全模块第一且锁定；自定义模块在安全之后、身份之前；空自定义省略。
  - 关键工具规则在全局模块与对应工具描述中同时存在。
  - plan full/brief/enter/exit 文案包含必要约束；`Prompt` 的稳定文本不包含环境/模式动态内容。
- `internal/environment`：
  - 非 Git 目录、detached HEAD、正常分支、modified/staged/untracked 计数、超时/失败降级。
  - `Info` 字段在 cwd/OS/shell/时间缺失时有明确未知值。
- `internal/message`：
  - supplement 标签格式、seq、mode/cadence 字段、`NextSupplementSeq` 最小 2、env seq=1。
  - `LastModeSupplement`、`HasFreshFullModeSupplement` 的真假分支。
  - `SanitizeSupplementBody` 处理闭合标签。
  - Anthropic 归一化：system block + cache_control、最后一个 tool cache_control、OpenAI 无 cache_control、supplement 为 user 文本。
- `internal/provider`：
  - Anthropic `message_start` 带缓存字段/不带字段；总输入计算；`CacheUsageKnown`。
  - OpenAI usage chunk 带/不带 `prompt_tokens_details`；cached_tokens；未知分支。
  - `Usage.Add` 与 `CacheHitRate`。
- `internal/agent`：
  - env supplement 置首、RefreshEnv 替换、无 supplement 补建、重复 env 清理。
  - plan cadence：第 1 轮完整（有/无 fresh transition supplement）、2–5 精简、第 6/11 完整；普通模式无 cadence。
  - supplement 不插在 tool_use/tool_result 之间；每轮快照包含 supplement。
  - 稳定 system 文本在整次运行中不变。
- `internal/context`：
  - 摘要 supplement 位置在 env 之后；env 保留；真实消息 keep 计数；tool_result 安全切分；空摘要/错误路径。
  - `ShouldSummarize` 使用传入的稳定 system 文本。
- `internal/session`：
  - 标题跳过 supplement；空标题不被时间戳提前占位；保存/恢复往返。
- `internal/tui`：
  - supplement 不渲染；`/plan` 追加 mode supplement 并落盘；`/resume`/`/clear`/`/new` 置 RefreshEnv。
  - 状态栏格式化：已知/未知/首轮写入/窄宽度降级。
  - `instructions.System()` 与 `prompt.New(profile.SystemPrompt).System()` 一致；`/switch`、`/resume` 后 `instructions` 被重建；`turnUsage` 在 startRun 重置。

### 集成测试

- 用 fake provider + fake env collector 跑一次多轮 agent：
  - 捕获每次请求的 system/tools/msgs。
  - 断言第一轮 env 在首位；plan 模式下第 1/6/11 轮看到完整文案；第 2–5 轮看到精简文案。
  - 断言 assistant tool_use 与 user tool_result 始终相邻，中间没有 supplement。
  - 断言稳定 system 与 tool 定义跨轮逐字节一致。
- TUI harness 测试：
  - 输入 `/plan` 后 `sess.Messages` 末尾出现 mode supplement；下一次 `startRun` 不再重复注入第 1 轮完整文案。
  - `/resume` 后旧 env supplement 被替换且仍在首位。
  - 状态栏在 `m.width` 较小时不超宽。
- 既有测试：`go test ./...`、`go vet ./...`、`gofmt -l`。

### 手动验证

- 按 spec R12 的 8 个场景逐条执行，记录到 tasks.md。
- 真实 Anthropic/OpenAI（或可用兼容网关）观察状态栏：
  - 稳定前缀达到最小缓存长度时，第二轮出现读取；
  - 追加 mode supplement 后读取不归零；
  - `/resume` 环境刷新后出现一次 cache creation，随后恢复读取；
  - 网关不返回字段时显示 `—`。
- 在窄终端（80 列以下）确认状态栏不换行破版。
- 在非 Git 目录启动确认 env supplement 降级且会话正常。
