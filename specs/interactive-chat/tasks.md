# 交互式对话（Interactive Chat）任务清单

状态：已完成

## 任务

- [x] T1：项目初始化与目录骨架
  - 依赖：无
  - 覆盖：基础设施（支撑全部 AC）
  - 完成标准：建立 go.mod（module nekocode）、`cmd/nekocode/main.go`（能编译运行、打印版本/帮助）、`internal/{config,provider,message,session,context,tui}` 空包占位；引入 bubbletea v2 / bubbles / lipgloss 依赖并锁定版本。
  - 验证方式：已执行 - `go build ./...` 通过；`go test ./internal/tui` 冒烟通过；`go run ./cmd/nekocode --version`/`--help` 正常输出；go.mod 锁定 charm.land v2 三依赖。

- [x] T2：配置层（加载 / 合并 / 校验 / 模板）
  - 依赖：T1
  - 覆盖：R1，R2，R3，R4，R5，R6，AC6，AC7，AC11
  - 完成标准：实现 `Config`/`Profile`/`ThinkingConfig` 结构与 YAML tag；`Load` 读全局 `~/.nekocode/config.yaml`，存在项目 `.nekocode.yaml` 时按名覆盖同名 profile；`active_profile` 解析顺序 `--profile` > 项目 > 全局；`Validate` 校验 protocol 取值、api_key 非空、profile 存在、budget_tokens≥1024 等；`GenerateTemplate` 生成带注释模板；检测到项目配置时返回需提示 `.gitignore` 的标记；默认 system prompt 常量。
  - 验证方式：已执行 - `go test ./internal/config` 通过（10 测试），`go vet` 通过；覆盖全局/项目合并/active 优先级/各类校验报错/模板生成/默认值。

- [x] T3：统一消息模型与归一化
  - 依赖：T2
  - 覆盖：R9，R10
  - 完成标准：实现 `Message`（Role/Content/Thinking/Signature/IsSummary/CreatedAt）；`Normalize(profile, system, []Message)` 按协议产出请求体：Anthropic 顶层 system 拼接、assistant 含 thinking+signature 时输出 `[{thinking},{text}]` 否则 `[{text}]`、max_tokens 缺省 4096、开 thinking 时 temperature 强制 1.0 且 max_tokens>budget_tokens；OpenAI messages 数组、system 消息保留、content 为纯文本、丢弃 thinking/signature。
  - 验证方式：已执行 - `go test ./internal/message` 通过（10 测试），`go vet` 通过；覆盖两家归一化、thinking 回传/丢弃、摘要 system、temperature/max_tokens 约束。

- [x] T4：Provider 接口与共享 SSE reader
  - 依赖：T3
  - 覆盖：R7，R8（SSE 部分）
  - 完成标准：定义 `Provider` 接口（`Stream(ctx, system, []Message) <-chan StreamEvent`）、`StreamEvent`（EventText/EventThinking/EventDone/EventError）、`New(Profile)` 工厂按 protocol 分发；实现 `sse.Reader`：按空行分事件、解析 `event:`/`data:`、多行 data 拼接、识别 OpenAI `data: [DONE]`、识别 Anthropic `event: error`。
  - 验证方式：已执行 - `go test ./internal/provider` 通过（SSE reader 9 测试，覆盖 event/data/多行 data/注释/[DONE]/无尾空行/CRLF/回调中断）。

- [x] T5：Anthropic Provider 实现（含 extended thinking）
  - 依赖：T4
  - 覆盖：R8，R10
  - 完成标准：`AnthropicProvider` 构建 `POST {base_url}/v1/messages`（header `x-api-key`/`anthropic-version`/`content-type`，body 走 Normalize，`stream:true`，含 thinking 配置）；解析 SSE 事件序列 `message_start`/`content_block_start`/`content_block_delta`(text_delta/thinking_delta/signature_delta)/`content_block_stop`/`message_delta`/`message_stop`/`ping`/`error`，转成统一 StreamEvent；signature 与 thinking 文本配对保存。
  - 验证方式：已执行 - `go test ./internal/provider` 通过（4 测试：text+thinking+signature 序列、error 事件、HTTP 401、ctx 取消）。

- [x] T6：OpenAI Provider 实现
  - 依赖：T4
  - 覆盖：R8，R10
  - 完成标准：`OpenAIProvider` 构建 `POST {base_url}/v1/chat/completions`（header `Authorization: Bearer`，body messages 数组含 system、`stream:true`）；解析 `data: {chunk}` 中 `choices[0].delta.content` 增量、`finish_reason`、`data: [DONE]`，转 EventText/EventDone；thinking 配置被忽略（不写入请求）。
  - 验证方式：已执行 - `go test ./internal/provider` 通过（5 测试：text+done、thinking 被忽略、HTTP 401、无 [DONE] 补发、ctx 取消）。

- [x] T7：会话存储层
  - 依赖：T3
  - 覆盖：R17，R18，R19，R20，R21
  - 完成标准：`Session` 结构（ID/Title/时间/CurrentProfile/ProfileHistory/Messages/EstTokens）；`Store` 实现 `Save`（写 `~/.nekocode/sessions/<id>.json`）、`Load`、`List`（扫描目录、按 UpdatedAt 倒序、取最近 20、损坏文件容错跳过）、标题生成（首条 user 截断 40 / 时间戳兜底）、`RecordSwitch` 记录 profile 切换。
  - 验证方式：已执行 - `go test ./internal/session` 通过（8 测试，覆盖存读往返/目录创建/列表倒序/损坏容错/limit/空目录/标题生成/切换记录）。

- [x] T8：上下文管理（估算 + 摘要）
  - 依赖：T4
  - 覆盖：R22，R23，R24，R25，AC8
  - 完成标准：`Estimate([]Message, system)` 按字符/系数（默认 4）求和；`ContextWindow(profile)` 解析 profile > 内置模型默认表 > 8k 兜底；`Summarize(ctx, provider, profile, []Message)`：达 80% 阈值时把旧消息请求模型压成一条 role=system/IsSummary 消息，保留 system_prompt + 最近 6 轮；返回新消息切片与已压缩轮数；摘要请求失败时回退不压缩并返回错误。
  - 验证方式：已执行 - `go test ./internal/context` 通过（9 测试，用 fake provider 验证估算/context_window 解析/触发/不触发/强制/过短/错误/空摘要/并入旧摘要）。

- [x] T9：TUI 骨架（状态 / 输入 / 渲染 / 主循环）
  - 依赖：T6，T7
  - 覆盖：R11，R12，R13，AC1，AC2
  - 完成标准：bubbletea `model`（messages/input/streamBuf/thinkBuf/streaming/profile/session）；`Init/Update/View`；`bubbles/textarea` 输入，Enter 提交、Shift+Enter 换行（终端不支持时备选 Alt+Enter/Ctrl+J）；View 渲染历史 + 输入框；按 Enter 把 user 消息加入会话并发起一轮请求（本任务可先用阻塞或最简流式打通端到端）。
  - 验证方式：已执行 - `go build`/`go vet` 通过；`go test ./internal/tui` 通过（渲染测试）；二进制冒烟 --version/--help/无配置生成模板/无效配置报错 正常。交互式流式待 T15 真实 key 验证。

- [x] T10：流式增量渲染 + thinking 区块
  - 依赖：T9
  - 覆盖：R8（展示），R16，AC3
  - 完成标准：`stream.go` 把 `Provider.Stream` 桥接为 `tea.Cmd`，每个 StreamEvent 转 tea.Msg；Update 收到 text delta 追加到 streamBuf 并触发重绘（逐字）；thinking delta 写入 thinkBuf，View 用 lipgloss 样式（灰度/缩进/分隔线）单独区块展示，与正式回答区分；EventDone 时把 streamBuf 落为 assistant 消息。
  - 验证方式：已执行 - stream.go 桥接 Provider.Stream→tea.Msg、view.go thinking 独立区块（💭 灰斜体）实现；`go test ./internal/tui` 通过。逐字/thinking 可视待 T15 手动确认。

- [x] T11：错误处理 + Ctrl+C 中断
  - 依赖：T10
  - 覆盖：R14，R26，R27，R28，AC9，AC10
  - 完成标准：StreamEvent EventError / HTTP 错误映射为可读提示（401/403 鉴权、429 限流、其它）；流式中 Ctrl+C `cancel()` 当前 context、保留已生成文本（标注不完整）回到输入态；空闲态 Ctrl+C 退出程序；非流式错误（模型不存在等）呈现不崩溃。
  - 验证方式：已执行 - EventError 保留已生成文本、Ctrl+C 流式中 cancel/空闲退出 实现；`go build`/`go vet` 通过。鉴权/中断待 T15 手动确认。

- [x] T12：slash 命令集
  - 依赖：T10，T8
  - 覆盖：R15，R20，R24
  - 完成标准：命令分发器实现 `/help` `/quit` `/clear` `/new` `/resume` `/switch [profile]` `/think` `/compact`；`/switch` 携带历史（按 R9 归一化、OpenAI 丢 thinking）并记录切换；`/think` 临时开关 thinking（仅 Claude，OpenAI 提示不支持）；`/compact` 调 T8 Summarize。
  - 验证方式：已执行 - `go test ./internal/tui` 通过（7 命令测试：clear/switch/think/resume/unknown）；`go build`/`go vet` 通过。

- [x] T13：会话恢复 + 自动摘要集成
  - 依赖：T12
  - 覆盖：R17，R18，R21，R23，R25，AC4，AC8
  - 完成标准：启动新建空会话；每轮结束自动落盘（R21）；`/resume` 列出最近 20 会话供选择、加载后继续；发送前调用 Estimate，达 80% 自动触发 Summarize 并在界面提示已压缩轮数；摘要消息在历史中以特殊样式呈现。
  - 验证方式：已执行 - 每轮落盘(saveSession)+自动摘要触发(TestAutoSummarizeTrigger)+/resume 列表加载测试 通过；`go build`/`go vet` 通过。交互式待手动。

- [x] T14：入口接线（flags / 首启动 / 配置引导）
  - 依赖：T13
  - 覆盖：R1，R4，R5，R6，AC6，AC7，AC11
  - 完成标准：`main.go` 解析 `--profile`；加载配置（全局+项目合并、active_profile 优先级）；全局配置不存在时生成模板并引导退出（R5）；项目配置检测到时打印 `.gitignore` 提示（R4）；配置无效清晰报错退出（R6）；启动 TUI。
  - 验证方式：已执行 - main.go 接线 --profile/配置加载/首启动模板(R5)/gitignore 提示(R4)/无效配置报错(R6)；二进制冒烟通过。AC6/AC7/AC11 完整验证在 T15。

- [x] T15：集成测试与最终验收
  - 依赖：T14
  - 覆盖：AC1–AC11（全部）
  - 完成标准：补充端到端 `httptest` 集成测试（Anthropic/OpenAI 各跑一轮含 thinking 的流式 + 一次摘要触发）；逐条对照 spec.md 验收标准在真实终端 + 真实/模拟 key 下验证；把结果回填本文件「最终验收」与「执行过的检查」。
  - 验证方式：已执行 - 集成测试（Anthropic/OpenAI 流式重建+error 保留 partial+自动摘要触发）通过；全量 78 测试通过；vet/build 通过。AC 逐条见最终验收。

## 最终验收

- AC1：通过（自动化）— TestIntegrationAnthropic/OpenAIStreamReconstruct 验证流式事件累积重建为 assistant 消息；逐字渲染由 bubbletea 增量重绘实现。交互式逐字观感待真实 key 手动确认。
- AC2：待手动 — 多轮记忆逻辑已实现（每轮发送 sess.Messages 全量历史）；需真实 key 验证 AI 引用前文。
- AC3：通过（自动化）— thinking+signature 捕获（T5+集成测试）+ 渲染区块（TestRenderMessage）。可视观感待手动确认。
- AC4：通过 — TestCommandResumeListAndLoad 验证 /resume 列表+加载；session 持久化测试验证落盘。
- AC5：通过（自动化）— /switch 切换+记录（TestCommandSwitch）+ 归一化丢弃 thinking（TestNormalizeOpenAIBasic）。跨 provider 连续对话待真实 key 手动确认。
- AC6：通过 — TestLoadProfileFlagOverrides 验证 --profile 优先级。
- AC7：通过 — TestProjectConfigMergesAndOverrides 验证项目 profile；gitignore 提示代码已实现（main.go）。
- AC8：通过 — T8 摘要测试 + TestAutoSummarizeTrigger 验证触发与生成。
- AC9：待手动 — Ctrl+C 中断代码已实现（Update ctrl+c -> cancel，保留已生成）；需交互式终端验证。
- AC10：通过 — provider 401 测试 + friendlyErr 映射 + 集成测试 EventError 保留 partial。
- AC11：通过 — 二进制冒烟：无配置首启动生成模板（R5）。

## 执行过的检查

- T1：`go build ./...` 通过；`go test ./internal/tui` 冒烟通过；`go run ./cmd/nekocode --version`/`--help` 正常；go.mod 锁定 charm.land v2 三依赖。
- T2：`go test ./internal/config` 通过（10 测试，覆盖全局/项目合并/active 优先级/各类校验报错/模板生成/默认值）；`go vet` 通过。
- T3：`go test ./internal/message` 通过（10 测试，覆盖两家归一化/thinking 回传与丢弃/摘要 system/约束）；`go vet` 通过。
- T4-T6：`go test ./internal/provider` 通过（18 测试：SSE reader 9 + Anthropic 4 + OpenAI 5，覆盖事件序列/thinking+signature/error/401/cancel/无[DONE]补发）；`go vet` 通过。
- T7：`go test ./internal/session` 通过（8 测试，覆盖存读/目录创建/列表倒序/损坏容错/limit/标题/切换记录）。
- T8：`go test ./internal/context` 通过（9 测试，覆盖估算/context_window/触发/强制/过短/错误/空摘要/并入旧摘要）。
- T9-T11、T14：`go build`/`go vet` 通过；`go test ./internal/tui` 通过（渲染测试）；二进制冒烟（--version/--help/无配置生成模板 R5/无效配置报错 R6）正常；stream 桥接+thinking 区块+错误/Ctrl+C+main 接线已实现。交互式流式待 T15 真实 key 验证。
- 全量最终：`go test ./... -count=1` 全绿（78 测试通过）；`go vet ./...`/`go build ./...` 通过；二进制冒烟（version/help/模板/报错）正常。

## 备注

- AC2/AC9 及 AC1/AC3/AC5 的交互观感需真实 API key 手动验证（本环境无 key，无法发起真实 LLM 调用）。手动步骤：① 填写 ~/.nekocode/config.yaml 的 api_key；② 运行 nekocode；③ 多轮对话验证记忆(AC2)；④ /think 验证思考区块(AC3)；⑤ /switch 验证跨 provider(AC5)；⑥ 流式中 Ctrl+C 验证中断(AC9)。
- 多行输入：Enter 发送、Ctrl+J 换行（Shift+Enter 在支持的终端也可换行，不支持时用 Ctrl+J）。
- OpenAI 兼容端点（DeepSeek/Ollama 等）：换 base_url+model+key 即可用（protocol=openai）。
- 后续可改进：viewport 滚动长对话、429 自动退避、OpenAI reasoning 模型专属展示。
