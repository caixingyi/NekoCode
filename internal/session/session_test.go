package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nekocode/internal/message"
)

func writeFile(t *testing.T, dir, id string, sess *Session) {
	t.Helper()
	b, err := json.Marshal(sess)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	sess := NewSession("claude")
	sess.Messages = []message.Message{
		{Role: message.RoleUser, Content: "你好"},
		{Role: message.RoleAssistant, Content: "你好呀", Thinking: "思考", Signature: "sig"},
	}

	if err := st.Save(sess); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.Load(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != sess.ID {
		t.Errorf("ID = %q", loaded.ID)
	}
	if loaded.CurrentProfile != "claude" {
		t.Errorf("CurrentProfile = %q", loaded.CurrentProfile)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("len(Messages) = %d", len(loaded.Messages))
	}
	if loaded.Messages[1].Thinking != "思考" || loaded.Messages[1].Signature != "sig" {
		t.Errorf("assistant message thinking/signature lost: %+v", loaded.Messages[1])
	}
	if loaded.Title != "你好" {
		t.Errorf("Title = %q, want 你好", loaded.Title)
	}
}

func TestSaveCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	st := NewStore(dir)
	sess := NewSession("claude")
	if err := st.Save(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.path(sess.ID)); err != nil {
		t.Errorf("session file not created: %v", err)
	}
}

func TestListSortByUpdatedAtDesc(t *testing.T) {
	dir := t.TempDir()
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)
	writeFile(t, dir, "s1", &Session{ID: "s1", UpdatedAt: t1})
	writeFile(t, dir, "s2", &Session{ID: "s2", UpdatedAt: t2})
	writeFile(t, dir, "s3", &Session{ID: "s3", UpdatedAt: t3})

	st := NewStore(dir)
	got, err := st.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	order := []string{got[0].ID, got[1].ID, got[2].ID}
	want := []string{"s3", "s2", "s1"}
	for i := range order {
		if order[i] != want[i] {
			t.Errorf("order = %v, want %v", order, want)
			break
		}
	}
}

func TestListCorruptFileTolerance(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good", &Session{ID: "good", UpdatedAt: time.Now()})
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 非 json 文件应被忽略。
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := NewStore(dir)
	got, err := st.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Errorf("got = %+v, want only [good]", got)
	}
}

func TestListLimit(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		writeFile(t, dir, "s"+string(rune('a'+i)), &Session{
			ID:        "s" + string(rune('a'+i)),
			UpdatedAt: base.Add(time.Duration(i) * time.Hour),
		})
	}
	st := NewStore(dir)
	got, err := st.List(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

func TestListEmptyDir(t *testing.T) {
	st := NewStore(t.TempDir())
	got, err := st.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty", got)
	}
}

func TestDeriveTitle(t *testing.T) {
	cases := []struct {
		name string
		msgs []message.Message
		want string
	}{
		{"first user", []message.Message{
			{Role: message.RoleAssistant, Content: "先有个回复"},
			{Role: message.RoleUser, Content: "帮我写代码"},
		}, "帮我写代码"},
		{"truncate long", []message.Message{
			{Role: message.RoleUser, Content: strings.Repeat("一", 50)},
		}, strings.Repeat("一", titleMaxRunes)},
		{"whitespace user falls back", []message.Message{
			{Role: message.RoleUser, Content: "   "},
		}, ""},
		{"no user falls back", []message.Message{
			{Role: message.RoleAssistant, Content: "x"},
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DeriveTitle(c.msgs)
			if c.want == "" {
				// 兜底应为时间戳格式（非空）。
				if got == "" {
					t.Errorf("fallback title empty")
				}
				return
			}
			if got != c.want {
				t.Errorf("title = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRecordSwitch(t *testing.T) {
	sess := NewSession("claude")
	sess.RecordSwitch("openai")
	if sess.CurrentProfile != "openai" {
		t.Errorf("CurrentProfile = %q", sess.CurrentProfile)
	}
	if len(sess.ProfileHistory) != 1 || sess.ProfileHistory[0].Profile != "openai" {
		t.Errorf("ProfileHistory = %+v", sess.ProfileHistory)
	}
	sess.RecordSwitch("claude")
	if len(sess.ProfileHistory) != 2 {
		t.Errorf("len(ProfileHistory) = %d, want 2", len(sess.ProfileHistory))
	}
}
