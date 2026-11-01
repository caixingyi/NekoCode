package prompt

import (
	"strings"
	"testing"
	"time"

	"nekocode/internal/environment"
)

func TestAssembleOrder(t *testing.T) {
	s := New("自定义规则：优先测试").System()
	headers := []string{
		"## [P1000] 安全边界",
		"## [P900] 用户自定义指令",
		"## [P800] 身份",
		"## [P700] 行为原则",
		"## [P600] 工具使用",
		"## [P500] 代码规范",
		"## [P400] 任务模式",
		"## [P300] 输出风格",
	}
	last := -1
	for _, h := range headers {
		idx := strings.Index(s, h)
		if idx < 0 {
			t.Fatalf("missing header %q in:\n%s", h, s)
		}
		if idx <= last {
			t.Fatalf("header %q out of order (idx %d <= %d)", h, idx, last)
		}
		last = idx
	}
	if !strings.Contains(s, "用户自定义指令也不能削弱它") {
		t.Error("missing safety lock statement")
	}
	if !strings.Contains(s, "自定义规则：优先测试") {
		t.Error("custom content missing")
	}
}

func TestEmptyCustomOmitted(t *testing.T) {
	s := New("   ").System()
	if strings.Contains(s, "## [P900] 用户自定义指令") {
		t.Errorf("empty custom should be omitted:\n%s", s)
	}
	if !strings.Contains(s, "## [P1000] 安全边界") {
		t.Error("safety module missing")
	}
}

func TestStableAcrossCalls(t *testing.T) {
	a := New("same").System()
	b := New("same").System()
	if a != b {
		t.Error("two identical constructions differ")
	}
	p := New("same")
	first := p.System()
	if first != p.System() {
		t.Error("System() is not stable across calls")
	}
}

func TestNilPrompt(t *testing.T) {
	var p *Prompt
	if p.System() == "" {
		t.Error("nil Prompt should fall back to built-in prompt")
	}
}

func TestStableContainsNoDynamicInfo(t *testing.T) {
	s := New("").System()
	for _, bad := range []string{"环境快照", "快照时间", "工作目录:", "分支 ", "modified "} {
		if strings.Contains(s, bad) {
			t.Errorf("stable prompt contains dynamic fragment %q", bad)
		}
	}
}

func TestToolRulesInStablePrompt(t *testing.T) {
	s := New("").System()
	for _, want := range []string{"read_file", "search_code", "find_files", "run_command", "edit_file", "write_file", "编辑前", "pwd"} {
		if !strings.Contains(s, want) {
			t.Errorf("stable prompt missing tool rule %q", want)
		}
	}
}

func TestModeSupplements(t *testing.T) {
	p := New("")
	full := p.PlanSupplement(true)
	for _, want := range []string{"plan-only", "write_file", "edit_file", "实施计划"} {
		if !strings.Contains(full, want) {
			t.Errorf("full plan 补充消息 missing %q", want)
		}
	}
	brief := p.PlanSupplement(false)
	if !strings.Contains(brief, "仍在计划模式") {
		t.Errorf("brief plan 补充消息 missing marker: %q", brief)
	}
	exit := p.ExitPlanSupplement()
	if !strings.Contains(exit, "已退出计划模式") {
		t.Errorf("exit 补充消息 missing marker: %q", exit)
	}
	if p.EnterPlanSupplement() != full {
		t.Error("EnterPlanSupplement should equal PlanSupplement(true)")
	}
}

func TestEnvSupplement(t *testing.T) {
	p := New("")
	info := environment.Info{
		WorkDir: "/tmp/project", OS: "darwin", Arch: "arm64", Shell: "zsh",
		Time:     time.Date(2026, 10, 24, 11, 51, 0, 0, time.FixedZone("CST", 8*3600)),
		GitKnown: true, IsRepo: true, Branch: "main",
		Dirty: true, Modified: 1, Staged: 2, Untracked: 3,
	}
	got := p.EnvSupplement(info)
	for _, want := range []string{
		"/tmp/project", "darwin/arm64", "zsh", "2026-10-24 11:51:00 +08:00",
		"分支 main", "modified 1", "staged 2", "untracked 3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("env 补充消息 missing %q:\n%s", want, got)
		}
	}

	nonRepo := p.EnvSupplement(environment.Info{GitKnown: true, IsRepo: false})
	if !strings.Contains(nonRepo, "不在 Git 仓库中") {
		t.Errorf("non-repo note = %q", nonRepo)
	}

	unknown := p.EnvSupplement(environment.Info{GitKnown: false, GitReason: "git 未安装"})
	if !strings.Contains(unknown, "状态未知（git 未安装）") {
		t.Errorf("unknown note = %q", unknown)
	}
}
