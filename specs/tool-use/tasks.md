# 工具系统（Tool Use）任务清单

状态：已完成

## 任务

- [x] T1：工具层骨架（接口/Result/Registry/路径守卫）
  - 依赖：无
  - 覆盖：R1，R2，R3，R13
  - 完成标准：`internal/tool/tool.go`（Tool 接口、Result、Registry 的 Register/Get/Definitions、执行超时）；`path.go` 的 resolvePath（限 cwd，拒绝 `../`/绝对路径逃逸，EvalSymlinks）。
  - 验证方式：待执行 - `go test ./internal/tool`（Registry 注册/查找/Definitions；resolvePath 正常/越界/symlink）。

- [x] T2：六个核心工具
  - 依赖：T1
  - 覆盖：R4，R5，R6，R7，R8，R9
  - 完成标准：read_file（超大按 ~2000 行截断）、write_file、edit_file（唯一匹配替换，0/多次报错）、run_command（sh -c，超时 30s，输出截断 ~30KB/200 行）、find_files（glob）、search_code（正则，返回 文件:行:内容）；各带 Schema 与 NeedsConfirm。
  - 验证方式：待执行 - `go test ./internal/tool`（每个工具正常/边界/越界/截断；edit 匹配 0/1/多）。

- [x] T3：消息模型扩展 + 归一化
  - 依赖：T1
  - 覆盖：R14
  - 完成标准：`Message` 加 `ToolCalls`/`ToolResults`；`ToolCall`/`ToolResult` 类型；`Normalize` 增加 tools 定义与两家工具块归一化（Anthropic tool_use blocks + tool_result user 消息；OpenAI tool_calls + role=tool），保证 id 配对与顺序。
  - 验证方式：待执行 - `go test ./internal/message`（两家工具块归一化、配对、顺序）。

- [x] T4：Provider 流式工具调用（Anthropic + OpenAI）
  - 依赖：T3
  - 覆盖：R15，R17
  - 完成标准：`Stream` 加 tools 参数；请求携带工具定义；SSE 解析工具调用并拼接分片 JSON 参数（Anthropic input_json_delta / OpenAI tool_calls.arguments），凑齐后发 `EventToolCall`；`StreamEvent` 加工具事件类型。
  - 验证方式：待执行 - `go test ./internal/provider`（httptest 模拟两家含 tool_use 的流，断言 EventToolCall 参数正确拼接）。

- [x] T5：TUI 单轮编排 + 确认 + 渲染
  - 依赖：T2，T4
  - 覆盖：R10，R11，R12，R16，R18
  - 完成标准：收到 tool_use 后进入「待确认」状态机，只读自动跑、写/改/执行 y/n/a 确认（run 展示命令、write 展示内容预览、edit 展示 diff）；拒绝回灌「已拒绝」；执行全部工具后 tool_result 回灌、再调一次得最终文本、停（第 2 次的 tool_use 不执行）；工具调用/结果渲染；ToolCall/ToolResult 随会话落盘。
  - 验证方式：待执行 - `go test ./internal/tui`（确认状态机、单轮编排逻辑用 fake provider）+ 手动。

- [x] T6：入口接线 + 集成测试与最终验收
  - 依赖：T5
  - 覆盖：R16，全部 AC
  - 完成标准：main 注入 registry(cwd)、把 tools 传入 Stream；补端到端 httptest 集成（两家「tool_use → 回灌 → 文本」全流程 + 多工具并行）；对照 AC1-AC8 验证并回填。
  - 验证方式：待执行 - `go test ./...` 全绿 + AC 逐条（交互项真实 key 手动）。

- [x] T7：助手回复 Markdown 渲染 + 工具摘要呈现（补记，对应后补的 R19/R20）
  - 依赖：T5
  - 覆盖：R19，R20，AC9，AC10
  - 完成标准：`internal/tui/markdown.go` 用 glamour（dark 主题、按宽度折行、按宽度缓存、失败回退原文）渲染**已完成**的助手消息，流式期间仍为纯文本；`renderMessage` 对工具调用/结果输出 `🔧 名 参数` / `✓|✗ 一行摘要`；新增 `renderMessage(msg, width)` 与 glamour 依赖。
  - 验证方式：`go test ./internal/tui`（renderMessage 传 width 的用例）+ 手动观感确认。

- [x] T8：修复 symlink 逃逸 + 补 renderMarkdown 单测（收尾）
  - 依赖：T1，T7
  - 覆盖：R13，AC6，R19，AC9
  - 完成标准：resolvePath 增加 EvalSymlinks（root 与目标路径已存在部分规范化），拦截 cwd 内软链指向外部的逃逸；`internal/tui/markdown_test.go` 直接单测 renderMarkdown（标题/列表/代码块/空输入/窄宽折行）。
  - 验证方式：`go test ./internal/tool`（新增 symlink 逃逸/内链放行两用例）+ `go test ./internal/tui`（markdown_test 直接覆盖 renderMarkdown）。

## 最终验收

- AC1：通过（自动化）— TestToolFlowAutoReadFile：read_file 自动执行，结果回灌得最终回答。
- AC2：通过（自动化）— T2 edit 0/1/多匹配报错；confirmPreview 生成 diff；TestToolFlowConfirmAllowWrites 写入成功。逐字观感待真实 key。
- AC3：通过（自动化）— TestToolFlowConfirmReject：run/write 需确认，拒绝回灌「已拒绝」。
- AC4：通过（自动化）— TestToolFlowMultipleTools：一次回复两个工具全执行、结果一起回灌。
- AC5：通过（自动化）— Anthropic 与 OpenAI(TestToolFlowOpenAIEndToEnd) 端到端均走通。
- AC6：通过（自动化）— TestToolsRejectEscape / TestResolvePathEscapeRejected / TestResolvePathSymlinkEscapeRejected：越界路径与软链逃逸被拒。
- AC7：通过（自动化）— 超时/edit 不匹配/文件不存在均结构化 Result，不崩溃。
- AC8：通过（自动化）— ToolCall/ToolResult 随 Message 落盘；/resume 由 step1 机制覆盖。交互观感待真实 key。
- AC9：通过（自动化）— markdown_test.go 直接单测 renderMarkdown（标题/列表/行内代码/代码块/空输入/窄宽折行）；Markdown 观感与折行仍待真实终端手动确认。
- AC10：通过（自动化）— renderMessage 对 ToolCalls 输出 `🔧 名 参数`、对 ToolResults 输出 `✓/✗ 一行摘要`。

## 执行过的检查

- T1：`go test ./internal/tool` 通过（Registry 注册/查找/Definitions 排序/超时；resolvePath 正常/越界/绝对路径）；`go vet` 通过。
- T2：`go test ./internal/tool` 通过（六工具正常/边界/越界；edit 0/1/多匹配；run 退出码；find glob；search 正则）；`go vet` 通过。
- T3：`go test ./internal/message` 通过（两家工具定义 + tool_use/tool_result 配对 + 多结果展开）；旧 10 处 Normalize 调用补 tools 参。
- T4：`go test ./internal/provider` 通过（两家分片 JSON 拼接成 ToolCall；OpenAI 多工具并行）；接口加 EventToolCall + tools 参，全库 Stream 调用点已更新。
- T5：`go test ./internal/tui` 通过（单轮编排：自动读/确认拒绝/允许写，确认状态机）。
- T6：`go test ./internal/tui`（OpenAI 端到端、多工具并行）；main 注入 registry(cwd)；二进制冒烟正常。
- 全量最终：`go test ./...` 全绿（104 PASS）；`go vet ./...` 通过。
- 交互观感（逐字/确认交互/长命令输出）待用户用真实 API key 手动确认。
- T7：`go build ./...`、`go test ./...` 全绿；renderMarkdown/欢迎页面貌待手动。
- T8：`go test ./internal/tool`（symlink 逃逸/内链放行两用例）；`go test ./internal/tui`（markdown_test 直接单测 renderMarkdown）通过。

## 备注

- Agent Loop（自动连环调用）按 spec 不做，留 step 3。
- 确认为 y/n/a；只读工具自动执行；工具限 cwd。
- 后续可加：危险命令黑名单、工具 profile 开关、并发执行、diff 高亮。
- 补记：R19/R20 已确认，spec.md 与 design.md 状态已转「已确认」。
- resolvePath 已加 EvalSymlinks 软链逃逸防护（T8），T1 完成标准中的「EvalSymlinks」已补齐。
