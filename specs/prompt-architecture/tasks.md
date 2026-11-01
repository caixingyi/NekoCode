# 提示词与指令架构 任务清单

状态：已完成

## 任务

- [x] T1：新增 `internal/prompt` 包
  - 依赖：无
  - 覆盖：R1，R2，R3，R5，AC1，AC2，AC9
  - 完成标准：`Prompt` 由 `New(custom)` 构造并缓存稳定文本；内置模块按 P1000→P300 确定性排序；安全模块锁定且第一；自定义模块在安全之后、身份之前；空自定义省略；关键工具规则写入全局工具模块；模式/环境 supplement 文案方法可用；同一 custom 的 `System()` 逐字节稳定。
  - 验证方式：已执行，`go test ./internal/prompt/...` 通过。

- [x] T2：扩展消息模型与 Anthropic 归一化
  - 依赖：无
  - 覆盖：R4，R7，AC3，AC5
  - 完成标准：`Message` 增加 `IsSupplement`/`SupplementKind`/`SupplementSeq`/`SupplementMode`/`SupplementCadence`；supplement 构造器生成 user 角色 + `<nekocode_supplement>` 标签；env seq=1，mode/summary seq>=2；`LastModeSupplement`、`HasFreshFullModeSupplement`、`SanitizeSupplementBody` 可用；Anthropic system 改为 block 数组并在 system 与最后一个 tool 上设置 `cache_control`；OpenAI 请求体不含 `cache_control`。
  - 验证方式：已执行，`go test ./internal/message/...` 通过。

- [x] T3：新增 `internal/environment` 包
  - 依赖：无
  - 覆盖：R6，AC4
  - 完成标准：`Collect` 返回 cwd/OS/架构/shell/快照时间/Git 分支/脏状态；非 Git、git 未安装、超时均降级为“未知/不在仓库”；`git status` 解析覆盖 modified/staged/untracked、detached HEAD、无提交仓库；采集有 500ms 超时。
  - 验证方式：已执行，`go test ./internal/environment/...` 通过。

- [x] T4：扩展 provider 缓存用量
  - 依赖：无
  - 覆盖：R10，AC8
  - 完成标准：`Usage` 增加 `CacheReadTokens`/`CacheCreationTokens`/`CacheUsageKnown`、`Add`、`CacheHitRate`；Anthropic 解析 `cache_read_input_tokens`/`cache_creation_input_tokens` 并计算总输入；OpenAI 解析 `prompt_tokens_details.cached_tokens`；缺失字段为 unknown 而不是 0。
  - 验证方式：已执行，`go test ./internal/provider/...` 通过。

- [x] T5：agent 接入 Prompt、env supplement 与 plan cadence
  - 依赖：T1，T2，T3
  - 覆盖：R6，R7，R8，AC2，AC4，AC5，AC6
  - 完成标准：`Config` 使用 `Prompt *prompt.Prompt` 并支持 `WorkDir`/`RefreshEnv`/`CollectEnv`；每次 Run 确保 env supplement 在首位，RefreshEnv 时替换并移动；第 1、6、11… 轮注入完整 plan supplement，其余轮精简，刚追加的完整进入提示不重复；普通模式无 cadence；supplement 不插入 tool_use/tool_result 之间；稳定 system 跨轮不变。
  - 验证方式：已执行，`go test ./internal/agent/...` 通过；TUI 既有测试已用 `realMessages` 适配 env supplement。

- [x] T6：context 摘要改造
  - 依赖：T2
  - 覆盖：R9，AC7
  - 完成标准：`ShouldSummarize`/`Summarize` 接收稳定 system 文本；摘要输出 `summary` supplement 且位于 env supplement 之后；env supplement 在压缩后保留；保留轮次只数真实消息；切分点向前移动以保持 tool_use/tool_result 配对；transcript 将 supplement 标为 `[系统补充]`；摘要 system prompt 要求保留当前模式/关键约束。
  - 验证方式：已执行，`go test ./internal/context/...` 通过。

- [x] T7：session 迁移与标题
  - 依赖：T2
  - 覆盖：R11，AC4，AC5
  - 完成标准：`DeriveTitle` 跳过补充消息；无真实用户消息时不提前写死时间戳标题；`/resume` 列表对空标题有兜底展示。
  - 验证方式：已执行，`go test ./internal/session/...` 通过。

- [x] T8：工具描述与 Registry.Root
  - 依赖：T1
  - 覆盖：R5，AC10
  - 完成标准：`read_file`/`edit_file`/`search_code`/`find_files`/`run_command` 描述包含与全局模块一致的关键约束；`Registry.Root()` 可用；测试断言关键短语在全局模块与工具描述中同时存在。
  - 验证方式：已执行，`go test ./internal/tool/... ./internal/prompt/...` 通过。

- [x] T9：TUI 接入
  - 依赖：T1，T2，T3，T4，T5，T6，T7，T8
  - 覆盖：R6，R7，R8，R10，R11，AC3，AC4，AC5，AC6，AC8，AC9
  - 完成标准：Model 持有 `instructions *prompt.Prompt`，在 `/switch`、`/resume` 后重建；`startRun` 传 Prompt/WorkDir/RefreshEnv 并重置 `turnUsage`；`/plan` 追加完整进入/退出 mode supplement；`/resume`、`/clear`、`/new` 置 `envNeedsRefresh`；`stream.go`/`update.go`/`saveSession` 使用 `instructions.System()`；supplement 不渲染；状态栏显示最近一轮缓存并在未知/窄终端下降级；`Registry.Root()` 提供工作目录。
  - 验证方式：已执行，`go test ./internal/tui/...` 通过；状态栏/模式 supplement/env 刷新均有单测。

- [x] T10：集成测试
  - 依赖：T5，T6，T9
  - 覆盖：AC2，AC4，AC5，AC6，AC7
  - 完成标准：fake provider + fake env collector 的多轮测试通过；断言 env 首位、plan cadence、工具配对、稳定前缀逐字节一致、summary 位置与 env 保留。
  - 验证方式：已执行，`go test ./internal/agent/...` 通过（`TestSupplementsSurviveSummarize` 覆盖 env/summary/工具配对/稳定 system）。

- [x] T11：人工行为与缓存评估
  - 依赖：T10
  - 覆盖：R12，AC10
  - 完成标准：spec R12 的 8 个场景逐条执行并记录实际结果；记录缓存状态栏观察；无法验证的项明确标注。
  - 验证方式：已执行（真实 profile=claude，protocol=anthropic，model=claude-sonnet-5）；8 个场景结果见“备注”。
  - 场景清单：
    - [x] 1. 工具偏好：使用 `search_code`/`find_files`/`read_file`，未用 `run_command`。
    - [x] 2. 编辑前读取：先 `read_file` 再 `edit_file`；文件已改为 `x * 3`。验证用 `run_command` 被 harness 拒绝，模型正确报告“未验证”。
    - [x] 3. plan 模式遵从：3 轮内未请求写类工具，输出完整计划；真实模型未跑到 6 轮，1/6/11 cadence 由单测覆盖。
    - [x] 4. 补充消息识别：未回复“收到系统提示”，未把 env supplement 当用户请求；更新规则后重跑不再调用 `run_command`。
    - [x] 5. 环境感知：cwd 与分支 main 回答正确，未调用工具。
    - [x] 6. 缓存命中：第二轮首轮 `cache_read=2176/2369`；多轮请求均有缓存读取；`cache_create` 字段始终为 0。
    - [x] 7. 恢复会话：env supplement 刷新为新 cwd/分支，历史中仅 1 条 env supplement；更新规则后重跑未调用工具。
    - [x] 8. 自定义 prompt：回答以【CUSTOM】开头，并明确安全边界 P1000 最高。

- [x] T12：最终检查与 SDD 收尾
  - 依赖：T11
  - 覆盖：AC1–AC10
  - 完成标准：`gofmt`、`go vet`、`go test ./...` 通过；tasks.md 勾选、最终验收与检查记录更新；spec/design 如有实现期变更先回写。
  - 验证方式：已执行，自动检查与 8 个真实场景均完成；临时场景脚本已删除。

## 最终验收

- AC1：通过（prompt 模块顺序/确定性测试）
- AC2：通过（prompt/agent 稳定前缀测试）
- AC3：通过（message 缓存断点与 OpenAI 无 cache_control 测试）
- AC4：通过（environment/agent/TUI env supplement 与刷新测试）
- AC5：通过（message/agent/TUI supplement 字段、隐藏、工具配对测试）
- AC6：通过（agent plan cadence 1/6/11 与 fresh transition 测试）
- AC7：通过（context/agent 集成：env 保留、summary 位置、安全切分）
- AC8：通过（provider 缓存用量解析、TUI 状态栏格式化测试）
- AC9：通过（prompt 自定义层、TUI 切换/恢复重建测试）
- AC10：通过（带说明：真实模型 plan 场景只跑到 3 轮，>5 轮 cadence 由单测覆盖；read_before_edit 的验证命令被 harness 拒绝，模型正确报告未验证；Anthropic 响应未报告 cache_create，但 cache_read 已观察到）

## 执行过的检查

- `go test ./...`：基线通过。
- `go test ./internal/prompt/...`：通过。
- `go test ./internal/environment/...`：通过。
- `go test ./internal/message/...`：通过。
- `go test ./internal/provider/...`：通过。
- `go test ./internal/agent/...`：通过。
- `go test ./...`：通过（T5 后）。
- `go test ./internal/context/...`：通过。
- `go test ./internal/session/...`：通过。
- `go test ./...`：通过（T7 后）。
- `go test ./internal/tool/... ./internal/prompt/...`：通过。
- `go test ./internal/tui/...`：通过（T9 后）。
- `go test ./... && go vet ./... && gofmt -l .`：通过（T9 后，gofmt 无输出）。
- `go test ./internal/agent/...`：通过（T10 集成测试）。
- `go build ./...`：通过。
- `go test -race ./...`：通过。
- `go vet ./...`：通过。
- `gofmt -l .`：无输出。
- `git diff --check`：通过。
- `go run ./cmd/prompt-scenarios`（临时脚本，真实 profile=claude / claude-sonnet-5）：8 个场景完成。
- 更新 `ls`/`pwd` 工具规则后重跑 `supplement_recognition`、`resume_env`：均不再调用 `run_command`。
- 临时脚本 `cmd/prompt-scenarios`：验收后删除。

## 备注

- spec 已确认；design/tasks 确认后从 T1 开始修改业务代码。当前阶段只允许改 `specs/prompt-architecture/`。
- 实现中若发现 spec/design 有遗漏，先更新对应文档，再继续。
- 用户已确认：Anthropic 不加移动消息断点；不新增 `cache_control` 开关；prompt 包封装、`profile.system_prompt` 配置字段保留。
- T1–T12 已完成；AC1–AC9 由自动测试覆盖，AC10 由真实模型场景覆盖。
- 真实场景结果摘要：
  - custom_prompt：通过；回答以【CUSTOM】开头，安全边界 P1000 最高。
  - tool_preference：通过；search_code → find_files → read_file，无 run_command。
  - read_before_edit：通过；read_file → edit_file，文件为 `x * 3`；验证命令被 harness 拒绝，模型如实报告未验证。
  - plan_mode：通过（带限制）；3 轮内无写类工具，输出目标/步骤/风险/验证计划；完整 cadence 的 1/6/11 由单测覆盖。
  - supplement_recognition：通过；未提“系统提示/系统补充”，更新规则后只用 find_files/read_file。
  - environment：通过；cwd 与 main 分支回答正确，未调用工具。
  - cache：通过；第二轮首轮 cache_read=2176/2369，多轮均有读取；cache_create 未报告。
  - resume_env：通过；env supplement 刷新为新 cwd/feature-resume，历史仅 1 条 env supplement；更新规则后未调用工具。
- 完整结果日志保留在 `/tmp/nekocode-scenarios-full.log` 与 `/tmp/nekocode-scenario-results-full.json`（仓库外）。
- 人工验收命令仍可用：`go run ./cmd/nekocode`；缓存观察状态栏“缓存 read/total pct%”。
