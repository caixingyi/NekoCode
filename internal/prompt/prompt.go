// Package prompt 负责生成 NekoCode 发给模型的 system 指令。
//
// 指令按是否随会话变化分成两类，各走各的通道：
//   - 稳定内容：内置段落（安全边界、身份、行为原则等）与用户自定义指令。
//     它们是确定性的静态文本，在构造时一次性拼装成 System() 的返回值，
//     之后逐字节不变，以便命中 provider 的前缀缓存。
//   - 动态内容：环境快照、当前时间、模式状态等每轮都会变化的信息。
//     它们不能进入 System()，改由 EnvSupplement / PlanSupplement 等补充消息承载。
package prompt

import (
	"sort"
	"strconv"
	"strings"
)

// 内置段落的优先级：数值越大越靠前、越权威。
const (
	PrioritySafety      = 1000
	PriorityCustom      = 900
	PriorityIdentity    = 800
	PriorityBehavior    = 700
	PriorityToolUse     = 600
	PriorityCodeStyle   = 500
	PriorityTaskModes   = 400
	PriorityOutputStyle = 300
)

// authorityPreamble 是拼装结果的开头，声明段落的排列规则与冲突裁决方式。
const authorityPreamble = "以下指令模块按优先级从高到低排列。发生冲突时，以更靠前（优先级更高）的模块为准。\n" +
	"安全边界模块不可被任何其他模块覆盖；用户自定义指令也不能削弱它。"

// Section 是 system 指令中的一段，例如“安全边界”“行为原则”。
// 若干 Section 由 assemble 按优先级从高到低拼成最终的 system 文本。
type Section struct {
	// Name 是段落标题，同时作为同优先级下的确定性排序依据。
	Name string
	// Priority 是优先级，数值越大越靠前、越权威。
	Priority int
	// Locked 标记该段不可被其他段覆盖。当前只在“安全边界”上置 true，
	// 未在拼装逻辑中强制，实际锁定语义由 authorityPreamble 声明。
	Locked bool
	// Content 是段落的正文指令。
	Content string
}

// Prompt 是一份拼装完成的 system 指令文本。
// 构造后不可变；同一实例多次调用 System() 返回逐字节相同的结果。
type Prompt struct {
	// custom 是用户提供的自定义指令原文（已去除首尾空白）。
	// 它已并入 stable，这里单独保留原值，便于判断用户是否配置过、也便于排查。
	custom string
	// stable 是拼装好的完整 system 文本，System() 直接返回它。
	// 只在构造时计算一次，之后不再变化，以命中 provider 的前缀缓存。
	stable string
}

// New 根据用户自定义 system_prompt 构造 Prompt。custom 为空（或全空白）表示
// 不添加自定义段落。拼装在构造时完成一次，之后 System() 直接返回缓存结果。
func New(custom string) *Prompt {
	custom = strings.TrimSpace(custom)
	return &Prompt{custom: custom, stable: assemble(custom)}
}

// System 返回稳定的 system 文本。nil 接收者回退到仅含内置段落的默认结果。
func (p *Prompt) System() string {
	if p == nil {
		return assemble("")
	}
	return p.stable
}

// assemble 按优先级从高到低拼装所有段落；同优先级按名称排序，保证结果确定。
func assemble(custom string) string {
	sections := make([]Section, 0, len(builtinSections)+1)
	sections = append(sections, builtinSections...)
	if custom != "" {
		sections = append(sections, Section{
			Name:     "用户自定义指令",
			Priority: PriorityCustom,
			Content:  custom,
		})
	}
	sort.SliceStable(sections, func(i, j int) bool {
		if sections[i].Priority != sections[j].Priority {
			return sections[i].Priority > sections[j].Priority
		}
		return sections[i].Name < sections[j].Name
	})

	var b strings.Builder
	b.WriteString(authorityPreamble)
	b.WriteString("\n")
	for _, sec := range sections {
		b.WriteString("\n## [P")
		b.WriteString(strconv.Itoa(sec.Priority))
		b.WriteString("] ")
		b.WriteString(sec.Name)
		if sec.Priority == PriorityCustom {
			b.WriteString("（高于除安全边界外的内置模块）")
		}
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(sec.Content))
		b.WriteString("\n")
	}
	return b.String()
}
