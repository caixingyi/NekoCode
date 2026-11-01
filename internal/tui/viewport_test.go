package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/message"
)

func fillLongHistory(m *Model, n int) {
	for i := 0; i < n; i++ {
		role := message.RoleUser
		if i%2 == 1 {
			role = message.RoleAssistant
		}
		m.sess.Messages = append(m.sess.Messages, message.NewMessage(
			role, fmt.Sprintf("MSG-%03d %s", i, strings.Repeat("long-content ", 8))))
	}
}

func viewLineCount(s string) int {
	return len(strings.Split(strings.TrimRight(s, "\n"), "\n"))
}

func TestViewportClipsLongHistoryAndScrolls(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 20
	fillLongHistory(m, 80)

	v := m.View()
	if n := viewLineCount(v.Content); n > m.height {
		t.Fatalf("view height = %d, want <= %d", n, m.height)
	}
	if !strings.Contains(v.Content, "MSG-079") {
		t.Error("latest message should be visible at the bottom")
	}
	if strings.Contains(v.Content, "MSG-000") {
		t.Error("oldest message should be clipped before scrolling")
	}

	// PgUp 到顶部：每次按键后调用 View 同步 viewport。
	for i := 0; i < 40 && !strings.Contains(m.View().Content, "MSG-000"); i++ {
		m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	}
	if !strings.Contains(m.View().Content, "MSG-000") {
		t.Error("oldest message should be visible after scrolling to top")
	}
	if m.followTail {
		t.Error("followTail should be false after scrolling up")
	}

	// PgDn 回到底部，followTail 恢复。
	for i := 0; i < 40 && !m.viewport.AtBottom(); i++ {
		m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
		m.View()
	}
	if !m.followTail {
		t.Error("followTail should be true at the bottom")
	}
	if !strings.Contains(m.View().Content, "MSG-079") {
		t.Error("latest message should be visible after scrolling back")
	}
}

func TestAltArrowScrollsHistory(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 20
	fillLongHistory(m, 60)
	m.View()
	bottom := m.viewport.YOffset()

	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModAlt}))
	m.View()
	up := m.viewport.YOffset()
	if up >= bottom {
		t.Fatalf("alt+up did not scroll up: offset %d -> %d", bottom, up)
	}
	if m.followTail {
		t.Error("followTail should be false after alt+up")
	}

	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown, Mod: tea.ModAlt}))
	m.View()
	down := m.viewport.YOffset()
	if down <= up {
		t.Fatalf("alt+down did not scroll down: offset %d -> %d", up, down)
	}
}

func TestPlainArrowsScrollWhenInputEmpty(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 20
	fillLongHistory(m, 60)
	m.View()
	bottom := m.viewport.YOffset()

	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m.View()
	up := m.viewport.YOffset()
	if up >= bottom {
		t.Fatalf("plain up did not scroll history with empty input: %d -> %d", bottom, up)
	}
	if m.followTail {
		t.Error("followTail should be false after plain up")
	}

	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.View()
	if m.viewport.YOffset() <= up {
		t.Errorf("plain down did not scroll history: %d -> %d", up, m.viewport.YOffset())
	}

	// 输入框有内容时，↑/↓ 仍归 textarea，不滚历史。
	m.input.SetValue("hello")
	before := m.viewport.YOffset()
	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m.View()
	if m.viewport.YOffset() != before {
		t.Errorf("input text should not scroll history: %d -> %d", before, m.viewport.YOffset())
	}
}

func TestMouseWheelScrollsHistory(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 20
	fillLongHistory(m, 60)
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want MouseModeCellMotion", got)
	}
	bottom := m.viewport.YOffset()

	mm, _ := m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	m = mm.(*Model)
	m.View()
	up := m.viewport.YOffset()
	if up >= bottom {
		t.Fatalf("wheel up did not scroll history: %d -> %d", bottom, up)
	}
	if m.followTail {
		t.Error("followTail should be false after wheel up")
	}

	mm, _ = m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	m = mm.(*Model)
	m.View()
	if m.viewport.YOffset() <= up {
		t.Errorf("wheel down did not scroll history: %d -> %d", up, m.viewport.YOffset())
	}
}

func TestViewportDoesNotYankWhenScrolledUp(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 20
	fillLongHistory(m, 60)
	m.View()

	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m.View()
	if m.followTail {
		t.Fatal("followTail should be false after PgUp")
	}

	m.sess.Messages = append(m.sess.Messages, message.NewMessage(message.RoleUser, "NEWEST-MARK"))
	if strings.Contains(m.View().Content, "NEWEST-MARK") {
		t.Error("new content should not yank the viewport to the bottom when scrolled up")
	}

	for i := 0; i < 40 && !m.viewport.AtBottom(); i++ {
		m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
		m.View()
	}
	if !m.followTail {
		t.Error("followTail should be true at the bottom")
	}
	if !strings.Contains(m.View().Content, "NEWEST-MARK") {
		t.Error("newest content should be visible at the bottom")
	}
}
