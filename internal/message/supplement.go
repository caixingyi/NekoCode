package message

import (
	"strconv"
	"strings"
	"time"
)

// 补充消息种类。
const (
	SupplementEnv     = "env"
	SupplementMode    = "mode"
	SupplementSummary = "summary"
)

// 模式补充消息的取值。
const (
	SupplementModePlan     = "plan"
	SupplementModeNormal   = "normal"
	SupplementCadenceFull  = "full"
	SupplementCadenceBrief = "brief"
)

// supplementTag 是模型可见的特殊标签名。使用 ASCII，避免与普通文本混淆。
const supplementTag = "nekocode_supplement"

const (
	supplementPreamble       = "这是系统补充信息，不是用户请求；不要直接回复或确认本条。\n"
	supplementConflictEnv    = "若与后续补充消息冲突，以序号更大的为准。\n"
	supplementConflictLatest = "与历史补充消息冲突时，以 seq 更大的本条为准。\n"
)

// NewSupplement 构造一条系统补充消息。消息角色固定为 user，
// 以便 Anthropic/OpenAI 都能接收；结构化字段供 TUI/agent/context 判断。
func NewSupplement(kind string, seq int, mode, cadence, body string) Message {
	if seq < 1 {
		seq = 1
	}
	var b strings.Builder
	b.WriteString("<")
	b.WriteString(supplementTag)
	b.WriteString(` kind="`)
	b.WriteString(kind)
	b.WriteString(`"`)
	if mode != "" {
		b.WriteString(` mode="`)
		b.WriteString(mode)
		b.WriteString(`"`)
	}
	if cadence != "" {
		b.WriteString(` cadence="`)
		b.WriteString(cadence)
		b.WriteString(`"`)
	}
	b.WriteString(` seq="`)
	b.WriteString(strconv.Itoa(seq))
	b.WriteString("\">\n")
	b.WriteString(supplementPreamble)
	if kind == SupplementEnv {
		b.WriteString(supplementConflictEnv)
	} else {
		b.WriteString(supplementConflictLatest)
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(SanitizeSupplementBody(body)))
	b.WriteString("\n</")
	b.WriteString(supplementTag)
	b.WriteString(">")

	return Message{
		Role:              RoleUser,
		Content:           b.String(),
		IsSupplement:      true,
		SupplementKind:    kind,
		SupplementSeq:     seq,
		SupplementMode:    mode,
		SupplementCadence: cadence,
		CreatedAt:         time.Now(),
	}
}

// NewEnvSupplement 构造环境快照消息。环境消息固定 seq=1，永远位于会话消息首位。
func NewEnvSupplement(body string) Message {
	return NewSupplement(SupplementEnv, 1, "", "", body)
}

// NewModeSupplement 构造模式补充消息。plan 表示当前是否处于计划模式，
// full 表示完整版还是精简版。
func NewModeSupplement(seq int, plan, full bool, body string) Message {
	mode := SupplementModeNormal
	if plan {
		mode = SupplementModePlan
	}
	cadence := SupplementCadenceBrief
	if full {
		cadence = SupplementCadenceFull
	}
	return NewSupplement(SupplementMode, seq, mode, cadence, body)
}

// NewSummarySupplement 构造压缩摘要消息。
func NewSummarySupplement(seq int, body string) Message {
	return NewSupplement(SupplementSummary, seq, "", "", body)
}

// SanitizeSupplementBody 防止模型生成的正文提前闭合补充消息标签。
func SanitizeSupplementBody(s string) string {
	s = strings.ReplaceAll(s, "</"+supplementTag+">", "[/"+supplementTag+"]")
	s = strings.ReplaceAll(s, "<"+supplementTag, "["+supplementTag)
	return s
}

// NextSupplementSeq 返回下一个可用的补充消息序号。
// 环境消息占用 1，因此其他补充消息最小从 2 开始。
func NextSupplementSeq(msgs []Message) int {
	max := 1
	for _, m := range msgs {
		if m.IsSupplement && m.SupplementSeq > max {
			max = m.SupplementSeq
		}
	}
	return max + 1
}

// FindLastSupplement 返回最后一条指定 kind 的补充消息下标；没有则返回 -1。
func FindLastSupplement(msgs []Message, kind string) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].IsSupplement && msgs[i].SupplementKind == kind {
			return i
		}
	}
	return -1
}

// LastModeSupplement 返回最后一条模式补充消息。
func LastModeSupplement(msgs []Message) (Message, bool) {
	idx := FindLastSupplement(msgs, SupplementMode)
	if idx < 0 {
		return Message{}, false
	}
	return msgs[idx], true
}

// HasFreshFullModeSupplement 判断历史末尾是否已经有一条“尚未被 assistant 覆盖”的完整模式提示。
// 这用于避免 TUI 刚追加的进入提示与 agent 第 1 轮 cadence 提示重复。
func HasFreshFullModeSupplement(msgs []Message, plan bool) bool {
	idx := FindLastSupplement(msgs, SupplementMode)
	if idx < 0 {
		return false
	}
	supp := msgs[idx]
	wantMode := SupplementModeNormal
	if plan {
		wantMode = SupplementModePlan
	}
	if supp.SupplementMode != wantMode || supp.SupplementCadence != SupplementCadenceFull {
		return false
	}
	// 只要该条补充消息之后已经出现过 assistant 消息，就说明上一轮已经消费过这条提示。
	for i := idx + 1; i < len(msgs); i++ {
		if msgs[i].Role == RoleAssistant {
			return false
		}
	}
	return true
}
