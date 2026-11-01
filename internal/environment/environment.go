// Package environment 采集当前会话的工作目录、系统信息与 Git 状态。
// 采集必须短超时、失败降级：环境信息只用于补充提示，不能阻塞会话启动。
package environment

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// gitTimeout 是单次 git status 的超时。大仓库上 git status 可能很慢，
// 超过这个时间就降级为“状态未知”，不阻塞首轮请求。
const gitTimeout = 500 * time.Millisecond

// Info 是一次环境快照。
type Info struct {
	WorkDir string
	OS      string
	Arch    string
	Shell   string
	Time    time.Time

	// GitKnown 表示 git 状态是否被成功采集；false 时 GitReason 说明原因。
	GitKnown  bool
	IsRepo    bool
	Branch    string
	Dirty     bool
	Modified  int
	Staged    int
	Untracked int
	GitReason string
}

// Collect 采集环境信息。workDir 为空时使用当前工作目录。
// 任何失败都降级为对应字段的“未知”状态，不返回 error。
func Collect(ctx context.Context, workDir string) Info {
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	info := Info{
		WorkDir: workDir,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Shell:   os.Getenv("SHELL"),
		Time:    time.Now(),
	}
	collectGit(ctx, workDir, &info)
	return info
}

// collectGit 用一次 git status --porcelain=v1 --branch 同时拿分支与脏状态。
func collectGit(ctx context.Context, workDir string, info *Info) {
	cctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "git", "-C", workDir, "status", "--porcelain=v1", "--branch")
	out, err := cmd.Output()
	if err != nil {
		switch {
		case errors.Is(cctx.Err(), context.DeadlineExceeded):
			info.GitReason = "git status 超时"
		case errors.Is(cctx.Err(), context.Canceled):
			info.GitReason = "采集被取消"
		case errors.Is(err, exec.ErrNotFound):
			info.GitReason = "git 未安装"
		default:
			// git 在非仓库目录会返回非零退出码；统一视为“不在 Git 仓库中”。
			info.GitKnown = true
			info.IsRepo = false
		}
		return
	}

	info.GitKnown = true
	info.IsRepo = true
	parseGitStatus(string(out), info)
}

// parseGitStatus 解析 `git status --porcelain=v1 --branch` 的输出。
// 第一行形如 "## main...origin/main [ahead 1]"；其余行是 XY 状态。
func parseGitStatus(out string, info *Info) {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			info.Branch = parseBranch(strings.TrimPrefix(line, "## "))
			continue
		}
		info.Dirty = true
		if strings.HasPrefix(line, "??") {
			info.Untracked++
			continue
		}
		if len(line) < 2 {
			continue
		}
		if line[0] != ' ' && line[0] != '?' {
			info.Staged++
		}
		if line[1] != ' ' && line[1] != '?' {
			info.Modified++
		}
	}
}

// parseBranch 从 --branch 行里提取分支名。
func parseBranch(s string) string {
	switch {
	case s == "":
		return ""
	case strings.HasPrefix(s, "No commits yet on "):
		return strings.TrimPrefix(s, "No commits yet on ")
	case strings.HasPrefix(s, "HEAD (no branch)"):
		return "detached HEAD"
	}
	// "main...origin/main [ahead 1]" / "main" / "main [behind 2]"
	if i := strings.Index(s, "..."); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	return s
}
