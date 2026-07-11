# 交互式对话（Interactive Chat）需求说明

状态：已确认

## 背景

NekoCode 是一个用 Go 开发的终端 AI 编程助手，目标是逐步演进到类似 Claude Code 的 coding agent。第一步先不做任何 agent 能力（工具调用、文件操作、代码编辑），只搭建**纯对话内核**：用户在终端启动后进入交互式 TUI，输入问题，NekoCode 调用大模型 API 并以 SSE 流式逐字打印回复，支持多轮对话与跨会话记忆。

这一步同时要奠定后续 agent 化的架构基础：Provider 层抽象为统一接口，先支持 Anthropic Claude 与 OpenAI 两家，通过 YAML 配置切换；并为 Claude 的 extended thinking 提供支持。把 Provider 抽象、消息归一化、会话持久化、上下文管理这些「骨架」在纯对话阶段打磨好，后续加 tool use 才不会推倒重来。

目标用户：开发者在终端里直接与 AI 对话，能在多家模型间切换、保留对话历史、看到 Claude 的思考过程。

## 用户故事

- 作为开发者，我希望启动 NekoCode 后直接进入对话界面输入问题，以便快速获得 AI 回复。
- 作为开发者，我希望回复是逐字流式打印的，以便不用等全部生成完就能开始阅读。
- 作为开发者，我希望 AI 记得同一会话里之前说过的话，以便进行连贯的多轮对话。
- 作为开发者，我希望下次启动能恢复之前的对话，以便继续未完成的话题。
- 作为开发者，我希望能通过配置在 Claude 和 OpenAI 之间切换，以便对比/使用不同模型。
- 作为开发者，我希望看到 Claude 的扩展思考过程，以便理解它的推理。
- 作为开发者，我希望对话太长时自动压缩旧内容，以便不被上下文窗口卡住。

## 需求

### 配置

- R1：NekoCode 读取全局配置 `~/.nekocode/config.yaml`；若当前工作目录存在 `.nekocode.yaml`，则作为项目本地配置，项目可定义专属 profile 并覆盖全局。`active_profile`（默认使用的 profile）解析优先级：`--profile` 启动参数 > 项目本地 > 全局。
- R2：配置顶层包含 `active_profile` 与 `profiles` 映射；每个 profile 包含四个核心字段 `protocol` / `model` / `base_url` / `api_key`，以及可选字段 `context_window` / `max_tokens` / `temperature` / `system_prompt` / `thinking`。若 profile 未设置 `system_prompt`，则注入一段默认的 NekoCode 身份说明。
- R3：`protocol` 取值为 `anthropic` 或 `openai`，决定走哪家协议；非法值启动时报错退出。
- R4：`api_key` 明文存于 yaml；当检测到项目本地配置文件时，提示用户将其加入 `.gitignore` 以防泄露。
- R5：全局配置文件不存在时，打印引导信息并生成一份带注释的模板配置后退出，提示用户填写后重启。
- R6：配置无效（如缺 api_key、protocol 非法、profile 不存在）时，给出清晰的错误信息并退出，不进入交互界面。

### Provider 抽象

- R7：Provider 抽象为统一接口，提供「流式对话」能力；新增后端只需实现该接口，不改动上层。
- R8：内置 Anthropic 与 OpenAI 两种 Provider 实现，均通过 SSE 流式返回。
- R9：NekoCode 维护统一的内部消息模型（system / user / assistant 角色，assistant 可附带 thinking 内容）；发送请求时按目标 provider 归一化：Anthropic 用顶层 system + content blocks（含 thinking block），OpenAI 用 messages 数组、content 为纯文本字符串、丢弃 thinking。
- R10：`thinking`（extended thinking）仅 `anthropic` protocol 生效，由 profile 的 `thinking` 字段控制开关与 budget_tokens；当 active profile 为 `openai` 但配置启用了 thinking 时，忽略并一次性提示用户该特性仅 Claude 支持。

### 交互 TUI

- R11：启动后进入交互界面，用户输入问题、回车发送，NekoCode 调用 active profile 对应 provider 流式逐字打印回复。
- R12：支持多轮对话，每次请求携带当前会话的历史消息。
- R13：多行输入默认通过 Shift+Enter 换行、Enter 发送（具体可行性在 design 阶段确认，必要时调整交互方式）。
- R14：流式生成过程中按 Ctrl+C 中断当前生成，已生成的文本作为该轮 assistant 回复保留。
- R15：支持 slash 命令：`/help`（帮助）、`/quit`（退出）、`/clear`（清空当前会话上下文）、`/new`（新建会话）、`/resume`（恢复历史会话）、`/switch [profile]`（切换 provider profile）、`/think`（临时开关 thinking，仅 Claude 生效）、`/compact`（手动触发上下文摘要压缩）。
- R16：当 thinking 启用时，思考内容在终端单独区块展示，与正式回答视觉区分（如不同颜色/缩进/分隔标记）。

### 会话持久化与恢复

- R17：每个会话以 JSON 文件持久化到 `~/.nekocode/sessions/<session_id>.json`，包含完整消息历史（含 thinking）与 metadata（创建/更新时间、使用过的 profile、消息数、估算 token 数）。
- R18：启动时默认新建一个空会话；用户可用 `/resume` 列出历史会话（按更新时间倒序，默认显示最近 20 条，含标题/时间/profile），选择后加载该会话继续对话。
- R19：会话标题默认取首条用户消息截断（约 40 字符），无用户消息时用时间戳兜底。
- R20：`/switch` 切换 profile 时携带当前历史消息到新 provider（按 R9 归一化，剥离 thinking），对话连续；切换不创建新会话，但在 metadata 中记录切换事件。
- R21：会话在每轮对话结束后自动落盘；异常退出后下次启动历史不丢（至多丢最后未完成的一轮）。

### 上下文管理

- R22：用字符数近似估算 token（系数可配置，默认约 1 token ≈ 4 字符）；`context_window` 优先取 profile 配置，缺失时查内置常见模型默认表，再缺失用保守默认值并提示。
- R23：当估算 token 达到 context_window 的 80%（阈值可配置）时，自动触发摘要压缩：将较旧的消息请求模型压缩成一段摘要消息，保留 system_prompt + 最近 6 轮（可配置）不压缩。
- R24：`/compact` 可随时手动触发同样的摘要压缩。
- R25：摘要压缩后，会话中以一条标注为「摘要」的消息替代旧消息，并在界面提示已压缩的轮数。

### 错误处理

- R26：流式生成中发生网络错误或 API 错误时，中断生成、保留已生成文本，给出错误提示，用户可重试该轮。
- R27：API 鉴权失败（401/403）时提示 api_key 或 base_url 可能有误；限流（429）时提示稍后重试（step 1 不自动退避重试）。
- R28：provider 返回非流式错误（如请求体非法、模型不存在）时，将错误信息呈现给用户，不崩溃。

## 验收标准

- AC1：填写有效全局配置后，运行 `nekocode` 进入交互界面，输入问题能收到流式逐字打印的回复。
- AC2：在同一会话中连续多轮提问，AI 能正确引用前文内容，证明记忆生效。
- AC3：配置一个 `protocol: anthropic` 且启用 thinking 的 profile，回复中出现与正式回答区分的独立思考区块。
- AC4：退出后重新启动，用 `/resume` 能列出历史会话并恢复，恢复后 AI 记得该会话的历史内容。
- AC5：会话中途 `/switch` 切到另一家 provider，后续对话携带历史（且 OpenAI 侧不出现 thinking 内容），对话连续。
- AC6：用 `--profile <name>` 指定不同 profile 启动，实际请求使用该 profile 的 protocol/model/base_url/api_key。
- AC7：在项目目录放置 `.nekocode.yaml` 定义专属 profile，NekoCode 能识别并可用该 profile（且提示加入 .gitignore）。
- AC8：人为制造超长对话接近 context_window，自动触发摘要压缩，压缩后仍能正常多轮对话且 AI 保留近期待压缩内容。
- AC9：流式生成中按 Ctrl+C，生成中断、已生成文本保留为该轮回复，界面回到可输入状态。
- AC10：配置错误的 api_key 启动并发送消息，得到清晰的鉴权失败提示而非崩溃。
- AC11：全局配置不存在时首次启动，生成模板配置并给出填写引导。

## 不做范围

- tool use / 函数调用、工具执行
- 文件读写、代码编辑、diff/patch、代码搜索等 agent 能力
- 多模态输入（图片等）
- 多账号、团队协作、云端会话同步
- 完整的费用/用量仪表盘（仅可在界面做极简提示，不做统计面板）
- 主题与配色深度定制（step 1 用合理默认样式）
- 自动更新机制
- OpenAI reasoning 模型专属展示（step 1 只做 Claude extended thinking；OpenAI 类思考输出按普通文本处理，不做特殊区块）

## 约束和假设

- 用 Go 实现；配置格式 YAML。
- 流式传输走 SSE。
- 单用户、单机、本地运行；假设用户网络可达配置的 base_url。
- 用户自备各 provider 的 api_key。
- step 1 不引入数据库，会话用 JSON 文件存储。
- token 计数为近似估算，不保证精确。

## 开放问题

- 多行输入：已确认默认 Shift+Enter 换行、Enter 发送；可行性在 design 阶段验证 TUI 库后确认，必要时调整。
- 默认 system prompt：已确认注入简短 NekoCode 身份说明，profile 的 system_prompt 可覆盖。
- 摘要阈值 80% / 保留 6 轮 / 估算系数 1 token≈4 字符：已确认为默认值（均可配置）。
- 429 限流：已确认 step 1 不做自动退避重试，仅提示。
- TUI 库选型与是否使用官方 Go SDK vs 直接 HTTP：留待 design 阶段决策。
