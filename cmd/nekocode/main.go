// Package main 是 nekocode CLI 的入口。
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"nekocode/internal/config"
	"nekocode/internal/session"
	"nekocode/internal/tui"
)

// helpText 是 --help 的输出。
const helpText = `NekoCode - 终端 AI 编程助手 (v%s)

用法:
  nekocode [flags]

flags:
  --profile <name>   使用指定 provider profile (覆盖 active_profile)
  --version, -v      打印版本并退出
  --help, -h         打印帮助并退出

交互中:
  Enter 发送 · Ctrl+J 换行 · Ctrl+C 中断/退出 · /help 查看命令

配置:
  全局: ~/.nekocode/config.yaml
  项目: ./.nekocode.yaml (可定义专属 profile 覆盖全局)
`

func main() {
	var (
		profile     string
		showVersion bool
		showHelp    bool
	)
	flag.StringVar(&profile, "profile", "", "使用的 provider profile (覆盖 active_profile)")
	flag.BoolVar(&showVersion, "version", false, "打印版本并退出")
	flag.BoolVar(&showVersion, "v", false, "打印版本并退出 (简写)")
	flag.BoolVar(&showHelp, "help", false, "打印帮助并退出")
	flag.BoolVar(&showHelp, "h", false, "打印帮助并退出 (简写)")
	flag.Parse()

	switch {
	case showVersion:
		fmt.Printf("nekocode %s\n", tui.Version)
		return
	case showHelp:
		fmt.Printf(helpText, tui.Version)
		return
	}

	// 加载配置（全局 + 项目合并、active_profile 解析、校验）。
	res, err := config.Load(config.LoadOptions{ProfileFlag: profile})
	if err != nil {
		if err == config.ErrConfigNotFound {
			// R5：全局配置不存在时生成模板并引导。
			path := config.DefaultGlobalPath()
			if gerr := config.GenerateTemplate(path); gerr != nil {
				fmt.Fprintf(os.Stderr, "生成模板配置失败: %v\n", gerr)
				os.Exit(1)
			}
			fmt.Printf("已生成模板配置: %s\n请填写 api_key 后重新运行 nekocode。\n", path)
			return
		}
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(1)
	}

	// R4：检测到项目本地配置时提示加入 .gitignore。
	if res.ProjectConfigPresent {
		fmt.Fprintln(os.Stderr, "提示: 检测到项目配置 .nekocode.yaml，建议加入 .gitignore 以防 api_key 泄露。")
	}

	store := session.NewStore(session.DefaultDir())
	m, err := tui.New(res.Config, res.ActiveProfile, store)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		os.Exit(1)
	}

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "运行失败:", err)
		os.Exit(1)
	}
}
