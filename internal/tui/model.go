// Package tui 实现基于 bubbletea v2 的交互式终端界面：
// 状态模型、事件处理、流式渲染、thinking 区块与 slash 命令。
package tui

import (
	"context"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"nekocode/internal/config"
	"nekocode/internal/message"
	"nekocode/internal/provider"
	"nekocode/internal/session"
	"nekocode/internal/tool"
)

// Version 是 NekoCode 版本号（可在构建时通过 -ldflags 覆盖）。
const Version = "0.1.0-dev"

// Model 是 TUI 的状态（MVU 中的 Model）。
type Model struct {
	cfg         *config.Config
	store       *session.Store
	registry    *tool.Registry
	profile     config.Profile
	profileName string
	sess        *session.Session

	input       textarea.Model
	streamBuf   string // 正在流式生成的回复文本
	thinkBuf    string // 正在流式生成的思考文本
	signature   string // 当前轮 thinking 签名
	streaming   bool
	pendingSend bool // 自动摘要后是否要继续发送
	streamCh    <-chan provider.StreamEvent
	cancel      context.CancelFunc

	// 工具单轮编排状态。
	pendingCalls   []message.ToolCall   // 本次 stream 收集到的工具调用
	confirmQueue   []message.ToolCall   // 待确认/执行的工具
	confirming     *message.ToolCall    // 正在等 y/n/a 的工具
	confirmText    string               // 确认预览文本
	toolResults    []message.ToolResult // 本轮累积结果
	toolExecuting  bool                 // 有工具执行中
	inSecondStream bool                 // 是否处于工具后的第二次 stream（此时 tool_use 不再执行）
	allowAll       bool                 // 本会话「全部允许」

	errMsg   string
	notice   string
	width    int
	height   int
	quitting bool
}

// New 构造 TUI Model。registry 提供工具（可为 nil，表示禁用工具）。
func New(cfg *config.Config, profileName string, store *session.Store, registry *tool.Registry) (*Model, error) {
	profile, err := cfg.Profile(profileName)
	if err != nil {
		return nil, err
	}
	ta := textarea.New()
	ta.Placeholder = "输入问题，Enter 发送，Ctrl+J 换行，/help 查看命令"
	ta.Prompt = "> "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	// 空输入时只显示 1 行 prompt，随内容增长至多 8 行（默认 Height=6 会渲染多行空 prompt）。
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 8
	ta.SetHeight(1)
	km := textarea.DefaultKeyMap()
	// Enter 留给提交；换行用 Ctrl+J / Alt+Enter / Shift+Enter（终端不支持 Shift+Enter 时用 Ctrl+J）。
	km.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "alt+enter", "shift+enter"))
	ta.KeyMap = km

	return &Model{
		cfg:         cfg,
		store:       store,
		registry:    registry,
		profile:     profile,
		profileName: profileName,
		sess:        session.NewSession(profileName),
		input:       ta,
	}, nil
}

// Init 实现 tea.Model。
func (m *Model) Init() tea.Cmd {
	return m.input.Focus()
}
