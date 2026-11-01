package prompt

import (
	"strconv"
	"strings"

	"nekocode/internal/environment"
)

// 模式提示文案。它们是动态补充消息的正文，不进入稳定 system。
const (
	modeFullPlan = `当前处于计划模式（plan-only）。
- 只使用只读工具（read_file、search_code、find_files）分析代码。
- 不要调用 write_file、edit_file、run_command 等有副作用的工具。
- 先充分阅读相关文件，再输出实施计划。
- 计划包含：目标、步骤、涉及文件、风险、验证方式。
- 如果写类工具被拦截，不要重试，直接输出计划并等待用户批准。`

	modeBriefPlan = `仍在计划模式：只做只读分析，不调用写类工具；最终输出实施计划。`

	modeExitPlan = `已退出计划模式。现在可以调用写类工具执行修改；写操作仍需用户确认。
按用户最新要求继续，不要沿用之前“只读/只输出计划”的限制。`
)

// EnterPlanSupplement 是进入 plan 模式时追加的完整提示。
func (p *Prompt) EnterPlanSupplement() string { return modeFullPlan }

// ExitPlanSupplement 是退出 plan 模式时追加的完整提示。
func (p *Prompt) ExitPlanSupplement() string { return modeExitPlan }

// PlanSupplement 返回每轮 plan 模式提示：full 为完整版，否则为精简版。
func (p *Prompt) PlanSupplement(full bool) string {
	if full {
		return modeFullPlan
	}
	return modeBriefPlan
}

// EnvSupplement 生成环境快照的正文；外层标签由 message 包负责。
// 该文本是动态的，只能放进会话补充消息，不能进入 System()。
func (p *Prompt) EnvSupplement(info environment.Info) string {
	var b strings.Builder
	b.WriteString("环境快照（会话开始或恢复时采集，时间与 Git 状态可能已过期）：\n")
	b.WriteString("- 工作目录: ")
	b.WriteString(orUnknown(info.WorkDir))
	b.WriteString("\n")

	osArch := strings.TrimSpace(info.OS)
	arch := strings.TrimSpace(info.Arch)
	switch {
	case osArch == "" && arch == "":
		osArch = "未知"
	case osArch == "":
		osArch = arch
	case arch == "":
		// keep osArch
	default:
		osArch = osArch + "/" + arch
	}
	b.WriteString("- 操作系统: ")
	b.WriteString(osArch)
	b.WriteString("\n")
	b.WriteString("- Shell: ")
	b.WriteString(orUnknown(info.Shell))
	b.WriteString("\n")

	when := "未知"
	if !info.Time.IsZero() {
		when = info.Time.Format("2006-01-02 15:04:05 -07:00")
	}
	b.WriteString("- 快照时间: ")
	b.WriteString(when)
	b.WriteString("\n")
	b.WriteString("- Git: ")
	b.WriteString(gitSummary(info))
	b.WriteString("\n")
	return strings.TrimRight(b.String(), "\n")
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未知"
	}
	return s
}

func gitSummary(info environment.Info) string {
	if !info.GitKnown {
		if info.GitReason != "" {
			return "状态未知（" + info.GitReason + "）"
		}
		return "状态未知"
	}
	if !info.IsRepo {
		return "不在 Git 仓库中"
	}
	branch := info.Branch
	if branch == "" {
		branch = "未知分支"
	}
	if !info.Dirty {
		return "分支 " + branch + "；工作区干净"
	}
	return "分支 " + branch + "；有未提交改动（modified " +
		strconv.Itoa(info.Modified) + ", staged " + strconv.Itoa(info.Staged) +
		", untracked " + strconv.Itoa(info.Untracked) + "）"
}
