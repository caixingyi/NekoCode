package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func validGlobal() string {
	return `
active_profile: claude
profiles:
  claude:
    protocol: anthropic
    model: claude-sonnet-5
    base_url: https://api.anthropic.com
    api_key: sk-ant-x
  gpt:
    protocol: openai
    model: gpt-4o-mini
    base_url: https://api.openai.com
    api_key: sk-openai-x
`
}

func TestLoadGlobalOnly(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, "config.yaml")
	writeYAML(t, gp, validGlobal())

	res, err := Load(LoadOptions{GlobalPath: gp, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.ActiveProfile != "claude" {
		t.Errorf("active = %q, want claude", res.ActiveProfile)
	}
	if res.ProjectConfigPresent {
		t.Errorf("project present = true, want false")
	}
	p, err := res.Config.Profile("claude")
	if err != nil {
		t.Fatal(err)
	}
	if p.Protocol != ProtocolAnthropic {
		t.Errorf("protocol = %q", p.Protocol)
	}
}

func TestLoadProfileFlagOverrides(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, "config.yaml")
	writeYAML(t, gp, validGlobal())

	res, err := Load(LoadOptions{GlobalPath: gp, WorkDir: t.TempDir(), ProfileFlag: "gpt"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ActiveProfile != "gpt" {
		t.Errorf("active = %q, want gpt", res.ActiveProfile)
	}
}

func TestProjectConfigMergesAndOverrides(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, "global.yaml")
	writeYAML(t, gp, validGlobal())
	pp := filepath.Join(dir, ".nekocode.yaml")
	writeYAML(t, pp, `
active_profile: local
profiles:
  claude:
    protocol: anthropic
    model: claude-opus-4
    base_url: https://api.anthropic.com
    api_key: sk-ant-x
  local:
    protocol: openai
    model: deepseek-chat
    base_url: https://api.deepseek.com
    api_key: sk-deepseek
`)

	res, err := Load(LoadOptions{GlobalPath: gp, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ProjectConfigPresent {
		t.Errorf("project present = false, want true")
	}
	if res.ActiveProfile != "local" {
		t.Errorf("active = %q, want local", res.ActiveProfile)
	}
	// 项目 profile 整体覆盖全局同名 profile。
	cp, _ := res.Config.Profile("claude")
	if cp.Model != "claude-opus-4" {
		t.Errorf("claude model = %q, want claude-opus-4", cp.Model)
	}
	// 全局其它 profile 保留。
	if _, err := res.Config.Profile("gpt"); err != nil {
		t.Errorf("gpt missing after merge: %v", err)
	}
	// 项目新增 profile 可用。
	lp, _ := res.Config.Profile("local")
	if lp.BaseURL != "https://api.deepseek.com" {
		t.Errorf("local base_url = %q", lp.BaseURL)
	}
}

func TestProjectActiveOverridesGlobal(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, "global.yaml")
	writeYAML(t, gp, validGlobal())
	pp := filepath.Join(dir, ".nekocode.yaml")
	writeYAML(t, pp, `
active_profile: gpt
profiles: {}
`)

	res, err := Load(LoadOptions{GlobalPath: gp, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.ActiveProfile != "gpt" {
		t.Errorf("active = %q, want gpt", res.ActiveProfile)
	}
}

func TestErrConfigNotFound(t *testing.T) {
	_, err := Load(LoadOptions{GlobalPath: filepath.Join(t.TempDir(), "nope.yaml"), WorkDir: t.TempDir()})
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestValidateErrors(t *testing.T) {
	gp := filepath.Join(t.TempDir(), "global.yaml")
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			"bad protocol",
			`active_profile: p
profiles:
  p: {protocol: gemini, model: m, api_key: k}
`,
			"protocol",
		},
		{
			"empty api_key",
			`active_profile: p
profiles:
  p: {protocol: openai, model: m, api_key: ""}
`,
			"api_key",
		},
		{
			"empty model",
			`active_profile: p
profiles:
  p: {protocol: openai, model: "", api_key: k}
`,
			"model",
		},
		{
			"budget too small",
			`active_profile: p
profiles:
  p: {protocol: anthropic, model: m, api_key: k, thinking: {enabled: true, budget_tokens: 100}}
`,
			"budget_tokens",
		},
		{
			"max_tokens <= budget",
			`active_profile: p
profiles:
  p: {protocol: anthropic, model: m, api_key: k, max_tokens: 2000, thinking: {enabled: true, budget_tokens: 2048}}
`,
			"max_tokens",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeYAML(t, gp, c.yaml)
			_, err := Load(LoadOptions{GlobalPath: gp, WorkDir: t.TempDir()})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %q, want substring %q", err.Error(), c.want)
			}
		})
	}
}

func TestActiveProfileNotFound(t *testing.T) {
	gp := filepath.Join(t.TempDir(), "global.yaml")
	writeYAML(t, gp, validGlobal())
	_, err := Load(LoadOptions{GlobalPath: gp, WorkDir: t.TempDir(), ProfileFlag: "nope"})
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("err = %v, want not-exist error", err)
	}
}

func TestNoActiveProfile(t *testing.T) {
	gp := filepath.Join(t.TempDir(), "global.yaml")
	writeYAML(t, gp, `
profiles:
  p: {protocol: openai, model: m, api_key: k}
`)
	_, err := Load(LoadOptions{GlobalPath: gp, WorkDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "active profile") {
		t.Fatalf("err = %v, want no-active error", err)
	}
}

func TestGenerateTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := GenerateTemplate(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		t.Fatalf("template parse: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("template validate: %v", err)
	}
	if cfg.ActiveProfile != "claude" {
		t.Errorf("active = %q", cfg.ActiveProfile)
	}
	if _, err := cfg.Profile("openai"); err != nil {
		t.Errorf("openai profile missing: %v", err)
	}
}

func TestEffectiveDefaults(t *testing.T) {
	p := Profile{Protocol: ProtocolAnthropic}
	if p.EffectiveBaseURL() != defaultAnthropicBaseURL {
		t.Errorf("base url = %q", p.EffectiveBaseURL())
	}
	if p.EffectiveMaxTokens() != DefaultMaxTokens {
		t.Errorf("max tokens = %d", p.EffectiveMaxTokens())
	}
	if p.EffectiveSystemPrompt() != DefaultSystemPrompt {
		t.Errorf("system prompt not default")
	}

	p2 := Profile{Protocol: ProtocolOpenAI, BaseURL: "https://x", MaxTokens: 100, SystemPrompt: "hi"}
	if p2.EffectiveBaseURL() != "https://x" {
		t.Errorf("base url = %q", p2.EffectiveBaseURL())
	}
	if p2.EffectiveMaxTokens() != 100 {
		t.Errorf("max tokens = %d", p2.EffectiveMaxTokens())
	}
	if p2.EffectiveSystemPrompt() != "hi" {
		t.Errorf("system prompt = %q", p2.EffectiveSystemPrompt())
	}
}
