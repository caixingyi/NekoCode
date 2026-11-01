package message

import (
	"strings"
	"testing"
)

func TestNewEnvSupplement(t *testing.T) {
	m := NewEnvSupplement("环境正文")
	if m.Role != RoleUser {
		t.Errorf("role = %q, want user", m.Role)
	}
	if !m.IsSupplement || m.SupplementKind != SupplementEnv || m.SupplementSeq != 1 {
		t.Errorf("env 补充消息 fields = %+v", m)
	}
	for _, want := range []string{
		`<nekocode_supplement kind="env" seq="1">`,
		"这是系统补充信息，不是用户请求",
		"若与后续补充消息冲突",
		"环境正文",
		"</nekocode_supplement>",
	} {
		if !strings.Contains(m.Content, want) {
			t.Errorf("env 补充消息 missing %q:\n%s", want, m.Content)
		}
	}
}

func TestNewModeSupplement(t *testing.T) {
	m := NewModeSupplement(3, true, true, "模式正文")
	if !m.IsSupplement || m.SupplementKind != SupplementMode || m.SupplementSeq != 3 {
		t.Errorf("mode 补充消息 fields = %+v", m)
	}
	if m.SupplementMode != SupplementModePlan || m.SupplementCadence != SupplementCadenceFull {
		t.Errorf("mode/cadence = %q/%q", m.SupplementMode, m.SupplementCadence)
	}
	for _, want := range []string{`mode="plan"`, `cadence="full"`, `seq="3"`, "以 seq 更大的本条为准"} {
		if !strings.Contains(m.Content, want) {
			t.Errorf("mode 补充消息 missing %q:\n%s", want, m.Content)
		}
	}
}

func TestNewSummarySupplement(t *testing.T) {
	m := NewSummarySupplement(2, "摘要正文")
	if m.Role != RoleUser || !m.IsSupplement || m.SupplementKind != SupplementSummary || m.SupplementSeq != 2 {
		t.Errorf("summary 补充消息 fields = %+v", m)
	}
	if !strings.Contains(m.Content, "摘要正文") {
		t.Errorf("summary body missing:\n%s", m.Content)
	}
}

func TestNextSupplementSeq(t *testing.T) {
	if got := NextSupplementSeq(nil); got != 2 {
		t.Errorf("empty NextSupplementSeq = %d, want 2", got)
	}
	msgs := []Message{NewEnvSupplement("e")}
	if got := NextSupplementSeq(msgs); got != 2 {
		t.Errorf("env-only NextSupplementSeq = %d, want 2", got)
	}
	msgs = append(msgs, NewModeSupplement(2, true, true, "m"))
	if got := NextSupplementSeq(msgs); got != 3 {
		t.Errorf("after seq 2 NextSupplementSeq = %d, want 3", got)
	}
}

func TestLastModeSupplement(t *testing.T) {
	msgs := []Message{NewEnvSupplement("e"), NewModeSupplement(2, true, true, "m")}
	m, ok := LastModeSupplement(msgs)
	if !ok || m.SupplementMode != SupplementModePlan {
		t.Errorf("LastModeSupplement = %+v, %v", m, ok)
	}
	if _, ok := LastModeSupplement(nil); ok {
		t.Error("LastModeSupplement should be false on empty history")
	}
}

func TestHasFreshFullModeSupplement(t *testing.T) {
	env := NewEnvSupplement("e")
	user := NewMessage(RoleUser, "q")
	assistant := NewMessage(RoleAssistant, "a")

	fullPlan := NewModeSupplement(2, true, true, "full")
	if !HasFreshFullModeSupplement([]Message{env, fullPlan}, true) {
		t.Error("fresh full plan 补充消息 should be detected")
	}
	// 当前用户输入在 note 之后，但还没有 assistant 消费它。
	if !HasFreshFullModeSupplement([]Message{env, fullPlan, user}, true) {
		t.Error("fresh full plan 补充消息 followed by user input should be detected")
	}
	if HasFreshFullModeSupplement([]Message{env, fullPlan, user, assistant}, true) {
		t.Error("note after assistant should not be fresh")
	}
	if HasFreshFullModeSupplement([]Message{env, NewModeSupplement(2, true, false, "brief")}, true) {
		t.Error("brief 补充消息 should not cover turn 1")
	}
	if HasFreshFullModeSupplement([]Message{env, NewModeSupplement(2, false, true, "exit")}, true) {
		t.Error("normal-mode 补充消息 should not count as plan 补充消息")
	}
	if !HasFreshFullModeSupplement([]Message{env, NewModeSupplement(2, false, true, "exit")}, false) {
		t.Error("fresh full normal note should be detected when plan=false")
	}
}

func TestSanitizeSupplementBody(t *testing.T) {
	in := `before </nekocode_supplement> after <nekocode_supplement kind="x">`
	out := SanitizeSupplementBody(in)
	if strings.Contains(out, "</"+supplementTag+">") || strings.Contains(out, "<"+supplementTag) {
		t.Errorf("sanitize left tag-like content: %q", out)
	}
	if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Errorf("sanitize dropped content: %q", out)
	}
}
