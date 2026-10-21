// Package session 负责会话的 JSON 持久化、列表、恢复与每轮落盘。
package session

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nekocode/internal/message"
)

// titleMaxRunes 是会话标题的最大字符数。
const titleMaxRunes = 40

// listDefaultLimit 是 /resume 默认列出的会话数上限（R18）。
const listDefaultLimit = 20

// ProfileSwitch 记录一次 provider profile 切换（R20）。
type ProfileSwitch struct {
	Profile string    `json:"profile"`
	At      time.Time `json:"at"`
}

// Session 是一次对话会话的完整状态（R17）。
type Session struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	CurrentProfile string            `json:"current_profile"`
	ProfileHistory []ProfileSwitch   `json:"profile_history"`
	Messages       []message.Message `json:"messages"`
	EstTokens      int               `json:"est_tokens"`
	// PlanMode 是会话级的计划模式开关（R14），随会话落盘，/resume 一并恢复。
	PlanMode bool `json:"plan_mode,omitempty"`
}

// Store 管理 ~/.nekocode/sessions/ 下的会话文件。
type Store struct {
	dir string
}

// NewStore 创建指向 dir 的 Store。
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// DefaultDir 返回默认会话目录 ~/.nekocode/sessions。
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".nekocode", "sessions")
}

// NewSession 创建一个新会话（R18：启动新建空会话）。
func NewSession(profile string) *Session {
	now := time.Now()
	return &Session{
		ID:             newID(),
		CreatedAt:      now,
		UpdatedAt:      now,
		CurrentProfile: profile,
	}
}

// RecordSwitch 记录一次 profile 切换（R20）。
func (s *Session) RecordSwitch(profile string) {
	s.CurrentProfile = profile
	s.ProfileHistory = append(s.ProfileHistory, ProfileSwitch{Profile: profile, At: time.Now()})
}

// Save 将会话写入 <dir>/<id>.json（R17、R21）。
// 每次保存更新 UpdatedAt；CreatedAt 为空则补齐；Title 为空则按首条用户消息生成。
func (s *Store) Save(sess *Session) error {
	if sess.ID == "" {
		return fmt.Errorf("session id 不能为空")
	}
	sess.UpdatedAt = time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = sess.UpdatedAt
	}
	if sess.Title == "" {
		sess.Title = DeriveTitle(sess.Messages)
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path(sess.ID), data, 0o600)
}

// Load 读取指定 id 的会话。
func (s *Store) Load(id string) (*Session, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// List 列出会话，按 UpdatedAt 倒序，取前 limit 条；损坏文件跳过（R18）。
// limit <= 0 时使用默认上限。
func (s *Store) List(limit int) ([]*Session, error) {
	if limit <= 0 {
		limit = listDefaultLimit
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue // 损坏文件容错跳过
		}
		out = append(out, &sess)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// DeriveTitle 按首条非空用户消息截断生成标题；无则用时间戳兜底（R19）。
func DeriveTitle(msgs []message.Message) string {
	for _, m := range msgs {
		if m.Role == message.RoleUser {
			t := strings.TrimSpace(m.Content)
			if t != "" {
				r := []rune(t)
				if len(r) > titleMaxRunes {
					r = r[:titleMaxRunes]
				}
				return string(r)
			}
		}
	}
	return time.Now().Format("2006-01-02 15:04")
}

// newID 生成形如 20060102-150405-<rand> 的会话 ID。
func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", time.Now().Format("20060102-150405"), b)
}
