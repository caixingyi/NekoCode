# Agent Loop（ReAct 循环）任务清单

状态：已完成

## 任务

- [x] T1：工具层扩展（只读声明 + panic 防护）
  - 依赖：无
  - 覆盖：R12，R26
  - 完成标准：`tool.Tool` 接口加 `IsReadOnly() bool`，六个工具各自实现（read/find/search=true，write/edit/run=false）；`Registry.Execute` 的执行 goroutine 内 `recover` 并把 panic 转成 `Result{OK:false}`；超时时区分「外部取消」与「超时」文案。
  - 验证方式：`go build ./...` + `go test ./internal/tool/` 通过（新增 TestRegistryExecuteRecoversPanic、TestRegistryExecuteCancelVsTimeout、TestBuiltinToolsReadOnly）。
  - 备注：原计划中的「修掉超时后 goroutine 泄漏」有误——`done` 本就是 buffered channel，不存在泄漏，已从 design/tasks 中更正。

- [x] T2：Provider 暴露停止原因与 token 用量
  - 依赖：无
  - 覆盖：R24，R25
  - 完成标准：`provider.StreamEvent` 加 `StopReason string` 与 `Usage *Usage`；新增统一停止原因常量 `StopEndTurn`/`StopToolUse`/`StopMaxTokens` 与两家归一化函数；Anthropic 解析 `message_start`（input tokens）、`message_delta`（stop_reason、output tokens）；OpenAI 请求带 `stream_options.include_usage`，解析 `finish_reason` 与末条 usage chunk；profile 增加可选 `stream_usage`（默认 true，可关）。
  - 验证方式：`go test ./internal/provider/`（新增 usage_test.go：两家 stop_reason/usage、缺 usage 降级、未知 finish_reason、stream_usage=false 不带该字段、归一化表驱动）。
  - 备注：归一化后 agent 只需认 `end_turn`/`tool_use`/`max_tokens` 三个值，未知值一律当 `end_turn`，空串表示服务端没回。

- [x] T3：agent 包骨架与单轮循环
  - 依赖：T1，T2
  - 覆盖：R1，R2，R3，R4，R5，R6，R7，R22
  - 完成标准：`internal/agent` 的 `event.go`（Event/EventType/StopReason）、`confirm.go`（Confirmer/Decision/Describe）、`agent.go`（Config/Agent/Run 起 goroutine 并 close channel、`commitTurn` 原子提交）、`loop.go`（单轮流式收集 → 终止判定 → 原子提交 EventTurnEnd 带历史快照）；终止判定覆盖 end_turn / 无工具调用 / 空响应 / max_tokens / 轮数上限（默认 25）/ 出错 / 取消。
  - 验证方式：`go test ./internal/agent/`（单轮无工具、多轮带工具且第二次请求历史配对、轮数上限、max_tokens 截断不执行工具、空响应不入库、上游错误、工具执行中取消、未知工具不中断、无 Confirmer 时写类被拒、allow-all 跨轮生效、Describe 预览）。
  - 备注：`guard.go` 的重复检测与 `loop.go` 的取消/上下文收尾随 T3 一并落地（循环结构上分不开）；T5 负责补齐这些路径的系统性验证。事件类型比 design 初稿多出 `EventToolRejected` / `EventToolBlocked`，供 TUI 区分「被拒绝」与「被 plan 拦截」。

- [x] T4：工具分批执行（并发读 + 串行写 + 按序回填 + 确认）
  - 依赖：T3
  - 覆盖：R10，R11，R13
  - 完成标准：`batch.go` 分类（`IsReadOnly() && !NeedsConfirm()` 才算可并发，未知工具与写类一律串行）→ 读批并发（信号量，默认上限 8）→ 屏障 → 写批按原始顺序串行；结果按**原始下标**写入数组（并发时各写各的位置），天然保序；写类逐个走 `Confirmer.Confirm`，拒绝后跳过本批剩余写类并各补「一并跳过」结果；`Confirmer` 为 nil 时写类一律按拒绝处理；批次号读批固定 0、写批固定 1，`EventToolResult` 带耗时。
  - 验证方式：`go test ./internal/agent/`（并发闸门证明三个读工具确实同时执行、MaxParallel 上限、读写混合时读批整体先于写批且回填保序、拒绝一个写后其余写全跳过而读批不受影响）；`go test -race` 通过。
  - 备注：`classify` 用「只读且无需确认」而非仅 `IsReadOnly()`——否则一个声明只读却要求确认的工具会在并发批里并发调用 `Confirmer`，写坏 `allowAll`。

- [x] T5：收场规则的验证与补强（重复检测 / 上下文超限 / 取消）
  - 依赖：T4
  - 覆盖：R8，R9，R18，R21
  - 完成标准：`guard.go` 的重复调用检测（工具名 + 规范化参数，忽略 key 顺序，连续 3 次相同 → `StopRepeatCall`，且在**执行前**判定）；注入式 `ContextLimit` 每轮结束检查 → `StopContextLimit`；ctx 取消在「流式中 / 部分 tool_use / 工具执行中 / 等待确认中」四个位置均走「补全结果 → 原子提交 → `StopCanceled`」路径，保证 tool_use 与 tool_result 数量始终相等。
  - 说明：代码随 T3/T4 一并落地（循环结构上分不开），本任务补齐了系统性验证。
  - 验证方式：`go test ./internal/agent/`（重复检测阈值表驱动含「读-写-读 不误杀」「参数不同不误杀」「同轮三次命中」；`callKey` 忽略参数顺序与嵌套顺序；上下文超限中止且只检查一次；自然结束时不做检查；四个取消点的对照测试 `TestCancelPointsAlwaysKeepHistoryPaired` + 等待确认中取消）；`go test -race` 通过。

- [ ] T6：plan-only 模式
  - 依赖：T4
  - 覆盖：R14，R15，R16，R17，R27
  - 完成标准：`plan.go` 提供统一拦截位，plan 模式下拦截写类并返回结构化结果（文案要求模型输出计划、不要重试）；不对 `Registry.Definitions()` 做裁剪；被拦调用计入重复检测；`session.Session` 加 `PlanMode` 持久化。
  - 验证方式：`go test ./internal/agent/`（读写同轮时读照跑写被拦、拦截不走确认、拦文案含工具名与「不要重试」、run_command 同样被拦、关掉开关恢复、被拦调用连续三次触发 repeat_call、工具定义全量下发）。
  - 备注：`/plan` 命令、状态栏标识与 `/resume` 恢复属于 TUI 侧，随 T7 的 TUI 改造一并做（T7 前 TUI 还没有 agent 接线）。

- [x] T7：TUI 改造为事件消费者
  - 依赖：T3，T4，T5，T6
  - 覆盖：R14，R19，R20，R22，R23
  - 完成标准：删除 `tools_flow.go` 的单轮编排；`agent_flow.go` 消费 agent 事件（渲染文本/thinking、轮次、工具耗时、token 用量、plan 拦截提示）；`confirmer.go` 实现 `Confirmer`（`sender.Send` + reply channel，`ctx` 取消时退出阻塞）；确认态按键 y/n/a、Esc=拒绝当前工具不取消循环，循环中 Esc/Ctrl+C 取消整个循环；`EventTurnEnd` 到达即用快照替换 `sess.Messages` 并落盘；`/plan` 命令与状态栏 `[PLAN]` 标识；`main.go` 接线 `SetProgram`。
  - 验证方式：`go test ./internal/tui/`（改写后的端到端：多轮循环、自动读、拒绝/允许/全部允许持久化、多工具、OpenAI 端到端、plan 拦截、Ctrl+C/Esc/y/a/n 键位映射、收场提示文案、快照落盘、plan 随 /resume 恢复、状态栏标识）；`go test -race` 通过。
  - 备注：新增了最小接口 `sender` 与 `Model.SetProgram`，否则 TUI 无法在不启动真实 Program 的情况下测试确认流程；`allowAll` 在 TUI 侧用 `atomic.Bool` 保存会话级开关（见 design「实现记录与设计偏差」）。

- [x] T8：集成测试与最终验收
  - 依赖：T7
  - 覆盖：全部 AC
  - 完成标准：agent 包单测覆盖全部终止条件、分批并发/串行、取消四位置、plan 拦截、panic 恢复；改造 step 2 的两家 httptest 端到端为多轮循环；`go test ./...` 全绿、`go vet ./...` 通过；对照 AC1–AC10 逐条验证并回填。
  - 验证方式：`go test -count=1 ./...`（158 个用例全绿）、`go vet ./...` 通过、`go test -race ./...` 通过。

## 最终验收

- AC1：通过（自动化）— `TestLoopContinuesAcrossTurns`（TUI 端到端两轮工具后给最终答复）、`TestLoopMultiTurnWithTool`（agent 侧断言第二次请求的历史里 tool_use 后紧跟配对 tool_result）。
- AC2：通过（自动化）— `TestReadsRunConcurrently`（并发闸门证明三个读工具确实同时执行）、`TestMaxParallelRespected`、`TestMixedBatchRunsReadsBeforeWrites`（读批整体先于写批，且回填顺序等于模型给出的顺序）。
- AC3：通过（自动化）— `TestDeniedWriteSkipsRemainingWrites`（拒绝一个写后其余写全跳过、读批不受影响、循环继续）、`TestToolFlowConfirmReject`（端到端文件未创建、结果含「拒绝」）。
- AC4：通过（自动化）— `TestCancelPointsAlwaysKeepHistoryPaired`（流式中 / 工具执行中 / 等待确认中三个取消点）、`TestLoopCancelBeforeAnyTool`、`TestCanceledHistoryCanContinue`（取消后的历史对两家协议都能 Normalize 通过，并能作为下一次 Run 的上下文续跑）；TUI 侧 `TestCtrlCDuringRunCancels`、`TestEscDuringRunCancels`、`TestEscInConfirmDeniesOnly`。
- AC5：通过（自动化）— `TestPlanModeBlocksWritesEndToEnd`（写未落盘、结果含「计划模式」、给出提示）、`TestPlanModeBlocksWritesButAllowsReads`、`TestPlanModeBlocksRunCommand`、`TestPlanModeOffExecutesWrites`、`TestPlanModePersistsAcrossResume`（`/new` 重置、`/resume` 恢复）。
- AC6：通过（自动化）— `TestLoopMaxTurns`、`TestLoopRepeatCallStops`、`TestLoopAlternatingCallsNotFlagged`（反例：交替调用不误杀）、`TestLoopMaxTokensTruncation`、`TestLoopEmptyResponse`、`TestLoopUpstreamError`、`TestContextLimitStopsLoop`；TUI `TestStopReasonNotices` 覆盖全部收场提示文案。
- AC7：通过（自动化）— `TestEventStreamMetadata`（逐项断言事件类型、轮次、批次号、是否需确认、工具耗时、本轮/累计用量、停止原因、历史快照）、`TestHeaderShowsTurnAndToolDuration`。
- AC8：通过（自动化）— `TestToolFlowOpenAIEndToEnd` 与 Anthropic 端到端均跑通多轮循环；`TestTokenUsageParsedAndShown`、`TestOpenAIUsageParsed`、`usage_test.go`（两家解析、缺 usage 降级、`stream_usage=false`）。
- AC9：通过（自动化）— `TestContextLimitStopsLoop`（中止且只检查一次，不发起下一轮）、`TestContextLimitNotCheckedWhenLoopEndsNaturally`；提示文案见 `TestStopReasonNotices`。
- AC10：通过（自动化）— `internal/agent` 全部用例使用 fake provider + fake tool，无网络、无 TUI、无文件 IO（`TestReadsRunConcurrently` 等用闸门而非 sleep 做确定性并发断言）。

## 执行过的检查

- T1：`go build ./...`、`go test ./internal/tool/` 通过。
- T2：`go test ./internal/provider/ ./internal/message/ ./internal/config/` 通过。
- T3–T6：`go test ./internal/agent/` 通过（循环、分批、收场、plan）。
- T7：`go test ./internal/tui/` 通过（改写后的端到端与键位测试）。
- 最终：`go test -count=1 ./...` 全绿（158 个用例，0 失败）；`go vet ./...` 通过；`go test -race ./...` 通过。
- 二进制冒烟：`go build ./cmd/nekocode` 成功，`--version` / `--help` 正常输出。
- 待人工确认（需真实 API key 与终端）：「多步任务跑通」的实际观感、并发读时界面的跳动、plan 模式下模型是否按提示转向输出计划、窄终端下的状态栏换行。

## 备注

- 本章是较大改动：单轮编排从 TUI 迁往 `internal/agent`，`tools_flow.go` 被重写。回归风险集中在确认流程与 `/resume` 的历史形状。
- 确认交互走 `Confirmer` 接口而非事件流（见 design 决策 3）；`Confirmer` 为 nil 时写类一律拒绝。
- 落盘边界改为「轮次原子提交」，落盘时刻永远是 tool_use/tool_result 配对的边界。
- 若实测发现 DeepSeek 一类兼容网关不支持 `stream_options`，用 profile 的 `stream_usage: false` 关掉。
