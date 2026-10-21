// Package config 负责读取、合并、校验 NekoCode 的 YAML 配置，
// 并在首次启动时生成模板配置。支持全局配置与项目本地配置的 profile 合并。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// 协议取值。
const (
	ProtocolAnthropic = "anthropic"
	ProtocolOpenAI    = "openai"
)

// DefaultSystemPrompt 是 profile 未设置 system_prompt 时注入的默认身份说明。
const DefaultSystemPrompt = "你是 NekoCode，一个运行在终端的 AI 助手。请直接、简洁地回答用户的问题。"

// 默认 base_url：profile 未设置时按 protocol 兜底。
const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultOpenAIBaseURL    = "https://api.openai.com"
)

// 默认 max_tokens（Anthropic 必填，Normalize 阶段使用）。
const DefaultMaxTokens = 4096

// thinking 最小预算（Anthropic 要求 >= 1024）。
const MinThinkingBudget = 1024

// ErrConfigNotFound 表示全局配置文件不存在，调用方应生成模板并引导用户（R5）。
var ErrConfigNotFound = errors.New("config not found")

// ThinkingConfig 控制 Claude extended thinking。
type ThinkingConfig struct {
	Enabled      bool `yaml:"enabled"`
	BudgetTokens int  `yaml:"budget_tokens"`
}

// Profile 描述一个 LLM 供应商配置。
type Profile struct {
	Protocol      string          `yaml:"protocol"`
	Model         string          `yaml:"model"`
	BaseURL       string          `yaml:"base_url"`
	APIKey        string          `yaml:"api_key"`
	ContextWindow int             `yaml:"context_window,omitempty"`
	MaxTokens     int             `yaml:"max_tokens,omitempty"`
	Temperature   *float64        `yaml:"temperature,omitempty"`
	SystemPrompt  string          `yaml:"system_prompt,omitempty"`
	Thinking      *ThinkingConfig `yaml:"thinking,omitempty"`
	// StreamUsage 控制 OpenAI 协议是否在请求里索要 token 用量
	// （stream_options.include_usage）。未设置时默认开启；部分兼容网关不支持
	// 该字段会直接报错，此时可显式设为 false。
	StreamUsage *bool `yaml:"stream_usage,omitempty"`
}

// EffectiveStreamUsage 返回流式请求是否索要 token 用量，未设置时默认开启（R25）。
func (p Profile) EffectiveStreamUsage() bool {
	if p.StreamUsage != nil {
		return *p.StreamUsage
	}
	return true
}

// EffectiveBaseURL 返回 base_url，为空时按 protocol 返回默认值。
func (p Profile) EffectiveBaseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return DefaultBaseURL(p.Protocol)
}

// EffectiveMaxTokens 返回 max_tokens，为 0 时返回默认值（用于 Anthropic 必填场景）。
func (p Profile) EffectiveMaxTokens() int {
	if p.MaxTokens > 0 {
		return p.MaxTokens
	}
	return DefaultMaxTokens
}

// EffectiveSystemPrompt 返回 system_prompt，为空时返回默认身份说明。
func (p Profile) EffectiveSystemPrompt() string {
	if p.SystemPrompt != "" {
		return p.SystemPrompt
	}
	return DefaultSystemPrompt
}

// Config 是 NekoCode 的顶层配置。
type Config struct {
	ActiveProfile string             `yaml:"active_profile"`
	Profiles      map[string]Profile `yaml:"profiles"`
}

// Profile 返回指定名称的 profile，不存在则报错。
func (c *Config) Profile(name string) (Profile, error) {
	if c.Profiles == nil {
		return Profile{}, fmt.Errorf("配置中没有定义任何 profile")
	}
	p, ok := c.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("profile %q 不存在", name)
	}
	return p, nil
}

// Validate 校验全部 profile 的结构合法性（R3、R6）。
func (c *Config) Validate() error {
	if len(c.Profiles) == 0 {
		return fmt.Errorf("配置中没有定义任何 profile")
	}
	for name, p := range c.Profiles {
		if err := validateProfile(name, p); err != nil {
			return err
		}
	}
	return nil
}

func validateProfile(name string, p Profile) error {
	switch p.Protocol {
	case ProtocolAnthropic, ProtocolOpenAI:
	default:
		return fmt.Errorf("profile %q: protocol %q 非法（应为 %q 或 %q）", name, p.Protocol, ProtocolAnthropic, ProtocolOpenAI)
	}
	if strings.TrimSpace(p.Model) == "" {
		return fmt.Errorf("profile %q: model 不能为空", name)
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("profile %q: api_key 不能为空", name)
	}
	if p.Thinking != nil && p.Thinking.Enabled {
		if p.Thinking.BudgetTokens < MinThinkingBudget {
			return fmt.Errorf("profile %q: thinking.budget_tokens 必须 >= %d", name, MinThinkingBudget)
		}
		// 开 thinking 时 max_tokens（缺省 DefaultMaxTokens）必须大于 budget_tokens。
		if p.EffectiveMaxTokens() <= p.Thinking.BudgetTokens {
			return fmt.Errorf("profile %q: max_tokens(%d) 必须大于 thinking.budget_tokens(%d)", name, p.EffectiveMaxTokens(), p.Thinking.BudgetTokens)
		}
	}
	return nil
}

// LoadOptions 控制 Load 的行为。
type LoadOptions struct {
	ProfileFlag string // --profile 值，空表示未指定
	WorkDir     string // 当前工作目录（用于定位项目配置），空表示 os.Getwd
	GlobalPath  string // 覆盖全局配置路径，空表示默认
	ProjectPath string // 覆盖项目配置路径，空表示基于 WorkDir 的默认值
}

// LoadResult 是 Load 的返回。
type LoadResult struct {
	Config               *Config
	ActiveProfile        string // 解析后的 active profile 名
	ProjectConfigPresent bool   // 是否加载了项目本地 .nekocode.yaml
}

// Load 读取全局配置，按需合并项目配置，解析 active profile 并校验（R1、R3、R6）。
// 全局配置不存在时返回 ErrConfigNotFound（R5 由调用方处理）。
func Load(opts LoadOptions) (*LoadResult, error) {
	globalPath := opts.GlobalPath
	if globalPath == "" {
		globalPath = DefaultGlobalPath()
	}

	data, err := os.ReadFile(globalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrConfigNotFound
		}
		return nil, fmt.Errorf("读取全局配置 %s: %w", globalPath, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析全局配置 %s: %w", globalPath, err)
	}

	// 合并项目本地配置（R1：项目 profile 按名整体覆盖全局同名 profile）。
	projectPresent := false
	projectPath := opts.ProjectPath
	if projectPath == "" {
		projectPath = DefaultProjectPath(opts.WorkDir)
	}
	if pdata, perr := os.ReadFile(projectPath); perr == nil {
		projectPresent = true
		pcfg := &Config{}
		if err := yaml.Unmarshal(pdata, pcfg); err != nil {
			return nil, fmt.Errorf("解析项目配置 %s: %w", projectPath, err)
		}
		mergeProfiles(cfg, pcfg)
		if pcfg.ActiveProfile != "" {
			cfg.ActiveProfile = pcfg.ActiveProfile
		}
	}

	// 解析 active profile：--profile > 项目 > 全局（R1）。
	active := opts.ProfileFlag
	if active == "" {
		active = cfg.ActiveProfile
	}
	if active == "" {
		return nil, fmt.Errorf("未指定 active profile（请设置 active_profile 或使用 --profile）")
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if _, err := cfg.Profile(active); err != nil {
		return nil, err
	}

	return &LoadResult{Config: cfg, ActiveProfile: active, ProjectConfigPresent: projectPresent}, nil
}

// mergeProfiles 用项目的 profile 整体覆盖全局同名 profile，新增的项目 profile 直接加入。
func mergeProfiles(dst, src *Config) {
	if dst.Profiles == nil {
		dst.Profiles = map[string]Profile{}
	}
	for name, p := range src.Profiles {
		dst.Profiles[name] = p
	}
}

// DefaultGlobalPath 返回全局配置路径 ~/.nekocode/config.yaml。
func DefaultGlobalPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".nekocode", "config.yaml")
}

// DefaultProjectPath 返回项目本地配置路径 <workDir>/.nekocode.yaml。
func DefaultProjectPath(workDir string) string {
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			workDir = "."
		}
	}
	return filepath.Join(workDir, ".nekocode.yaml")
}

// DefaultBaseURL 按 protocol 返回默认 base_url。
func DefaultBaseURL(protocol string) string {
	switch protocol {
	case ProtocolAnthropic:
		return defaultAnthropicBaseURL
	case ProtocolOpenAI:
		return defaultOpenAIBaseURL
	}
	return ""
}

// GenerateTemplate 在指定路径写入带注释的模板配置（R5）。
func GenerateTemplate(path string) error {
	tmpl := `# NekoCode 配置文件
# 全局位置: ~/.nekocode/config.yaml
# 项目本地可放置 .nekocode.yaml 覆盖/新增 profile（记得加入 .gitignore）

# 默认使用的 profile 名（也可启动时用 --profile 覆盖）
active_profile: claude

profiles:
  claude:
    protocol: anthropic          # anthropic | openai
    model: claude-sonnet-5
    base_url: https://api.anthropic.com
    api_key: your-anthropic-api-key
    # 可选字段：
    # max_tokens: 4096
    # temperature: 1.0           # 开启 thinking 时必须为 1.0
    # context_window: 200000
    # system_prompt: ""
    # thinking:
    #   enabled: false
    #   budget_tokens: 2048      # >= 1024，且 < max_tokens

  openai:
    protocol: openai
    model: gpt-4o-mini
    base_url: https://api.openai.com
    api_key: your-openai-api-key
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	if err := os.WriteFile(path, []byte(tmpl), 0o600); err != nil {
		return fmt.Errorf("写入模板配置: %w", err)
	}
	return nil
}
