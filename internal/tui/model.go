// Package tui 实现基于 bubbletea v2 的交互式终端界面：
// 状态模型、事件处理、流式渲染、thinking 区块与 slash 命令。
//
// 从 step 3 起，循环本体搬到了 internal/agent，本包退化为事件消费者：
// 负责渲染事件、把用户按键转成确认决定、以及把 agent 回带的历史快照落盘。
package tui

import (
	"context"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"nekocode/internal/agent"
	"nekocode/internal/config"
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

	input textarea.Model

	// 一次循环（agent.Run）的状态。
	eventCh <-chan agent.Event
	cancel  context.CancelFunc
	running bool
	// waiting 保证同一时刻只有一个 goroutine 在读事件 channel。
	waiting bool
	turn    int

	streamBuf string // 当前轮正在流式生成的回复文本
	thinkBuf  string // 当前轮正在流式生成的思考文本
	signature string

	executing int // 正在执行的工具数（并发读可能大于 1）

	// 确认：agent 通过 sender 发来的待确认请求。
	confirm *confirmRequestMsg
	// allowAll 是「本会话全部允许」开关。由确认器跨 goroutine 读，
	// 因此用 atomic；一旦打开，后续写操作不再询问（R13）。
	allowAll atomic.Bool

	// 统计与状态展示。
	turnUsage  provider.Usage
	totalUsage provider.Usage
	lastTool   string
	lastExec   time.Duration
	blocksSeen int // 本轮被 plan 模式拦截的次数

	stopReason agent.StopReason
	sender     sender

	pendingSend bool   // 自动摘要后是否要继续发送
	pendingText string // 待发送的输入

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

// SetProgram 注入 bubbletea Program，用于把确认请求投回 UI 事件循环。
// main 在 tea.NewProgram 之后、Run 之前调用；不调用时写类工具按安全默认拒绝。
func (m *Model) SetProgram(p *tea.Program) {
	m.sender = programSender{p: p}
}

// Init 实现 tea.Model。
func (m *Model) Init() tea.Cmd {
	return m.input.Focus()
}
