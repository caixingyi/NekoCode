# 交互式对话（Interactive Chat）技术设计

状态：草稿

## 总体方案

整体分四层，自底向上：

1. **配置层 `internal/config`**：加载全局 `~/.nekocode/config.yaml` 与项目 `.nekocode.yaml`，合并、校验、生成模板。多 profile 结构，解析 active profile。
2. **Provider 层 `internal/provider`**：定义统一 `Provider` 接口（流式对话），内置 Anthropic / OpenAI 两种实现，共享一个 SSE 读取器。**不使用各家官方 SDK，直接 `net/http` + 手写 SSE 解析**，保证两家实现形态一致、抽象干净。
3. **领域层 `internal/message` / `internal/session` / `internal/context`**：统一消息模型与会话归一化、JSON 会话持久化与恢复、token 估算与自动摘要压缩。
4. **表现层 `internal/tui`**：基于 **bubbletea v2 + bubbles + lipgloss** 的 MVU 界面，textarea 多行输入、viewport 滚动输出、thinking 独立区块、流式增量渲染、Ctrl+C 中断、slash 命令。

入口 `cmd/nekocode/main.go` 负责 flag 解析（`--profile`）、加载配置、启动 TUI。

数据流：用户输入 → TUI 发起 `tea.Cmd`（goroutine 内调用 Provider.Stream）→ Provider 读 SSE 解析成统一流事件 → 通过 channel 回传 `tea.Msg` → Model 增量更新 → View 重绘。每轮结束后领域层落盘会话、估算 token、必要时触发摘要。

## 需求对应关系

**配置**
- R1（全局+项目覆盖、active_profile 优先级）：`config.Load` 先读全局，再读项目本地（若存在），项目 profile 按名覆盖全局同名 profile；active_profile 解析顺序 `--profile` > 项目 > 全局。
- R2（profile 字段 + 默认 system prompt）：`Profile` 结构含 4 核心字段 + 可选字段；`system_prompt` 为空时注入默认身份说明（`config.DefaultSystemPrompt`）。
- R3（protocol 取值校验）：加载时校验 `protocol ∈ {anthropic, openai}`，非法即报错退出。
- R4（明文 key + gitignore 提示）：检测到项目 `.nekocode.yaml` 时，首次打印一行 `.gitignore` 提示。
- R5（首启动生成模板）：全局配置不存在时写 `~/.nekocode/config.yaml` 模板（含两个注释示例 profile）并退出。
- R6（配置无效报错）：缺 api_key、profile 不存在、protocol 非法等均集中校验、清晰报错退出。

**Provider 抽象**
- R7（统一接口）：`Provider` 接口 + 工厂 `New(cfg Profile) (Provider, error)`，新后端只需实现接口。
- R8（双实现 SSE）：`AnthropicProvider` / `OpenAIProvider` 各自构建 HTTP 请求、共享 `sse.Reader` 读取。
- R9（消息归一化）：`message` 包定义统一 `Message`，`Normalize` 函数按 protocol 转成各家请求体（见数据模型）。
- R10（thinking 仅 Claude）：Anthropic 实现支持 thinking；OpenAI 实现忽略 thinking 配置，首次使用时经 TUI 提示一次。

**交互 TUI**
- R11/R12（流式逐字、多轮）：`Provider.Stream(ctx, messages) <-chan StreamEvent`；TUI 把每个 text delta 追加到当前 assistant 缓冲并重绘；每轮把完整历史发给 provider。
- R13（多行输入）：`bubbles/textarea` + 自定义 KeyMap：Enter 提交、Shift+Enter 换行；终端不支持时备选 Alt+Enter / Ctrl+J。
- R14（Ctrl+C 中断）：拦截 Ctrl+C，若正在流式则 `cancel()` 该轮 context、保留已生成文本；空闲时退出程序。
- R15（slash 命令）：TUI 内命令分发器，`/help /quit /clear /new /resume /switch /think /compact`。
- R16（thinking 单独区块）：流式 thinking delta 写入独立缓冲，lipgloss 样式（灰度+缩进+分隔线）与正式回答区分。

**会话持久化**
- R17（JSON 落盘）：`session.Store` 读写 `~/.nekocode/sessions/<id>.json`，含 messages + metadata。
- R18（启动新建 + /resume）：启动新建空会话；`/resume` 扫描 sessions 目录、按 UpdatedAt 倒序取最近 20、列出供选择。
- R19（标题）：首条 user 消息截断 40 字符，无则时间戳。
- R20（/switch 携带历史）：切换 profile 不新建会话，记录切换事件；后续请求按 R9 归一化（OpenAI 丢弃 thinking）。
- R21（每轮落盘）：每轮 assistant 完成后立即写盘；异常退出至多丢最后一轮。

**上下文管理**
- R22（token 估算）：`context.Estimate(messages)` 按字符数 / 系数（默认 4）求和；context_window 取 profile > 内置模型默认表 > 保守默认（8k）。
- R23（80% 自动摘要）：发送前估算，达阈值则 `Summarize`：旧消息请求模型压成一条 summary，保留 system + 最近 6 轮。
- R24（/compact 手动）：复用 `Summarize`。
- R25（摘要消息 + 提示）：摘要存为 role=system 的特殊消息，界面提示已压缩轮数。

**错误处理**
- R26（流中错误）：SSE error 事件 / 网络错误 → `StreamEvent{Type: EventError}` → TUI 中断、保留已生成、提示可重试。
- R27（401/403/429）：HTTP 状态码映射为可读提示；429 提示稍后重试，不自动退避。
- R28（非流式错误）：请求体非法 / 模型不存在等，错误信息呈现不崩溃。

## 技术决策

- **决策 1：Provider 用直接 HTTP + 自写 SSE，不引入官方 SDK。**
  - 理由：用户要求 Provider 抽象干净、便于加后端；两家 SSE 格式都简单；直接 HTTP 让 Anthropic / OpenAI 实现形态完全一致，新后端只需实现接口 + 写请求/解析，不依赖异构 SDK。共享 `sse.Reader` 复用 `data:` 行解析。
  - 未采用：官方 `anthropic-sdk-go` / `openai-go`——会引入两套不同抽象，且 SDK 的流式迭代器与 thinking 类型封装反而让统一接口更绕；step 1 不需要 SDK 的高级特性（重试/批处理）。

- **决策 2：TUI 用 bubbletea v2 + bubbles + lipgloss。**
  - 理由：Go 生态事实标准，MVU 适合流式增量更新；bubbles 提供 textarea/viewport 现成组件；lipgloss 做样式。模块路径为 `charm.land/{bubbletea,bubbles,lipgloss}/v2`，已验证 v2.0.8 / v2.1.1 / v2.0.5 在 go1.26 编译通过。
  - 未采用：tview（组件全但非 MVU、流式集成更重）/ 纯 termbox——前者抽象与流式 command 模型不太契合，后者太底层。

- **决策 3：统一消息模型存储 thinking 文本 + signature，发送时按 provider 归一化。**
  - 理由：Anthropic 多轮 thinking 建议回传带 signature 的 thinking block；OpenAI 不支持则丢弃。内部统一存储，归一化时各取所需。
  - 未采用：始终丢弃 thinking 不回传——会丢失 Claude 多轮推理连续性，且 R9 明确要求 Anthropic 侧含 thinking block。

- **决策 4：会话用 JSON 文件存储，无数据库；/resume 扫描目录。**
  - 理由：step 1 单机单用户，会话量小，JSON 可读易调试；扫描目录取最近 20 足够。
  - 未采用：SQLite——step 1 过重，后续会话量大了再迁移。

- **决策 5：token 近似估算（字符/4）+ profile context_window + 内置模型默认表。**
  - 理由：用户已选；近似估算免依赖 tokenizer，触发摘要用保守阈值即可，不要求精确。
  - 未采用：调 provider 计数接口——OpenAI 无公开计数端点，不一致且增延迟。

- **决策 6：摘要结果存为 role=system 的特殊消息。**
  - 理由：两家都支持 system；归一化时 Anthropic 把所有 system 拼进顶层 system，OpenAI 原样插入 system 消息，统一且无角色污染。
  - 未采用：用 user 消息承载摘要——会污染对话角色语义。

> 以上决策 1、2 是较大选型，**提请你确认**；若你倾向官方 SDK 或其它 TUI 库，我在此调整后再写 tasks.md。

## 影响范围（新增文件）

```
NekoCode/
├── cmd/nekocode/main.go              # 入口：flag、加载配置、启动 TUI
├── internal/
│   ├── config/config.go              # 加载、合并、校验、模板
│   ├── provider/
│   │   ├── provider.go               # Provider 接口、StreamEvent、工厂
│   │   ├── sse.go                    # 共享 SSE reader
│   │   ├── anthropic.go              # Anthropic 实现（含 thinking）
│   │   └── openai.go                 # OpenAI 实现
│   ├── message/message.go            # Message 模型、Normalize
│   ├── session/session.go            # 会话存储、列表、恢复、落盘
│   ├── context/context.go            # token 估算、Summarize
│   └── tui/
│       ├── model.go                  # bubbletea Model、状态
│       ├── update.go                 # 事件处理、命令、slash 分发
│       ├── view.go                   # 渲染（含 thinking 区块）
│       └── stream.go                 # Provider.Stream → tea.Msg 桥接
├── go.mod / go.sum
└── specs/interactive-chat/           # SDD 文档（本目录）
```

## 数据模型 / 接口变化

**配置（YAML → Go）**

```go
type Config struct {
    ActiveProfile string             `yaml:"active_profile"`
    Profiles      map[string]Profile `yaml:"profiles"`
}
type Profile struct {
    Protocol      string          `yaml:"protocol"`        // anthropic | openai
    Model         string          `yaml:"model"`
    BaseURL       string          `yaml:"base_url"`
    APIKey        string          `yaml:"api_key"`
    ContextWindow int             `yaml:"context_window,omitempty"`
    MaxTokens     int             `yaml:"max_tokens,omitempty"`
    Temperature   *float64        `yaml:"temperature,omitempty"`
    SystemPrompt  string          `yaml:"system_prompt,omitempty"`
    Thinking      *ThinkingConfig `yaml:"thinking,omitempty"`
}
type ThinkingConfig struct {
    Enabled      bool `yaml:"enabled"`
    BudgetTokens int  `yaml:"budget_tokens"` // >=1024
}
```

**统一消息模型**

```go
type Role string // "system" | "user" | "assistant"

type Message struct {
    Role      Role       `json:"role"`
    Content   string     `json:"content"`           // 文本
    Thinking  string     `json:"thinking,omitempty"`// Claude 思考文本
    Signature string     `json:"signature,omitempty"`// thinking 签名（回传 Anthropic 用）
    IsSummary bool       `json:"is_summary,omitempty"`
    CreatedAt time.Time  `json:"created_at"`
}
```

归一化（`message.Normalize`）：
- Anthropic：system 消息拼接为顶层 `system`；assistant 若有 Thinking+Signature → `[{type:"thinking",thinking,signature},{type:"text",text:Content}]`，否则 `[{type:"text",text:Content}]`；user → `[{type:"text",text}]`。请求必填 `max_tokens`（缺省 4096）；开 thinking 时 temperature 强制 1.0 且 `max_tokens > budget_tokens`。
- OpenAI：system 消息保留为 `{role:"system"}`；assistant/user → `{role, content: Content}`，丢弃 Thinking/Signature。

**Provider 接口**

```go
type StreamEvent struct {
    Type  EventType // EventText | EventThinking | EventDone | EventError
    Text  string
    Err   error
}
type Provider interface {
    Stream(ctx context.Context, sys string, msgs []Message) <-chan StreamEvent
}
func New(p config.Profile) (Provider, error) // 按 protocol 分发
```

**会话存储（JSON）**

```go
type Session struct {
    ID            string         `json:"id"`
    Title         string         `json:"title"`
    CreatedAt     time.Time      `json:"created_at"`
    UpdatedAt     time.Time      `json:"updated_at"`
    CurrentProfile string        `json:"current_profile"`
    ProfileHistory []ProfileSwitch `json:"profile_history"`
    Messages      []message.Message `json:"messages"`
    EstTokens     int            `json:"est_tokens"`
}
```

## 边界情况

- 空配置 / 首启动：生成模板退出（R5）。
- 项目本地 profile 与全局同名：项目覆盖全局（深合并可选字段，核心字段整体替换）。
- OpenAI 下配置了 thinking：忽略 + 一次性提示（R10）。
- thinking 开启但 budget_tokens < 1024：校验报错。
- 多轮中混用开/关 thinking 的 assistant 轮：有 Thinking 的带 signature 回传，无的仅 text，均合法。
- 跨 provider 携带历史到 OpenAI：丢弃 thinking/signature，仅文本。
- Ctrl+C 中断：context cancel，已生成文本保留为该轮 assistant（可能不完整，标注）。
- 流式中途网络断开 / SSE error 事件：转 EventError，保留已生成，提示重试。
- 摘要请求本身失败：回退为不压缩 + 提示，不丢消息。
- 会话目录不存在 / 单文件损坏：跳过损坏文件，/resume 列表容错。
- 极短上下文（消息少于保留轮数）：不触发摘要。
- 终端不支持 Shift+Enter：备选 Alt+Enter / Ctrl+J 换行。
- max_tokens 缺省：Anthropic 默认 4096；OpenAI 不传（用模型默认）。

## 风险

- **Shift+Enter 终端兼容性**：部分终端不区分 Shift+Enter，多行输入交互可能受影响 → 备选按键 + 文档说明；design 已留备选。
- **bubbletea v2 模块路径已迁移**：API 可能微变（如 KeyMsg → KeyPressMsg）→ 锁定具体版本，关注 UPGRADE_GUIDE。
- **Ctrl+C 既要中断流又要可退出**：需自定义处理覆盖默认退出 → 实现时用 context 控制，验证空闲态能正常退出。
- **token 估算不准**：可能过早/过晚触发摘要 → 用保守阈值 80%，并在界面提示当前估算用量，必要时手动 /compact。
- **Anthropic thinking 多轮回传签名缺失**：必须妥善保存 signature 否则回传会被拒 → 流式时捕获 signature_delta，校验非空再回传。
- **直接 HTTP 的健壮性**：无 SDK 的重试/边界处理 → step 1 显式处理常见错误码，复杂重试延后。

## 测试策略

- 单元测试：
  - config：加载/合并/校验/模板生成、active_profile 优先级、各类无效配置报错。
  - message.Normalize：Anthropic（含/不含 thinking、system 拼接、max_tokens/temperature 约束）、OpenAI（丢弃 thinking）双方向。
  - sse：`data:` 行解析、`[DONE]`、多行 data、空行、error 事件。
  - context.Estimate：字符系数估算；Summarize：触发阈值、保留轮数、摘要消息生成（provider 用 fake）。
  - session：写盘/读取/列表排序/损坏文件容错/标题生成。
- 集成测试：
  - 用 httptest.Server 模拟 Anthropic / OpenAI SSE，端到端跑 Stream → 事件序列正确（text/thinking/done/error）。
  - 摘压缩流程：构造超长历史 → 触发 → 请求体含旧消息、返回摘要、会话消息被替换。
- 手动验证：对应 AC1–AC11 逐条在真实终端 + 真实/模拟 API key 下验证（见 tasks.md 最终验收）。
