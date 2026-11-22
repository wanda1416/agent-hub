package cli

import (
	"fmt"
	"io"
	"os"

	"agenthub/internal/control"
)

// Options configures a CLI invocation.
type Options struct {
	DataDir string
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
}

const usageText = `AgentHub 命令行工具

用法：
  agenthub process list                        列出全部进程
  agenthub process start <id>                  启动进程
  agenthub process stop <id>                   停止进程
  agenthub process logs <id> [选项]            查看进程日志
      --tail N                                仅显示最后 N 行（默认 100）
      --follow, -f                            持续输出新增日志
  agenthub config get                          查看当前配置
  agenthub config set theme <light|dark>       设置明暗主题
  agenthub config set language <zh|en>         设置界面语言
  agenthub config set autostart <on|off>       设置开机自动启动
  agenthub config set autostart-wsl <on|off>   设置开机时自动启动 WSL
  agenthub wsl status                          查看 WSL 运行状态
  agenthub wsl start                           启动 WSL
  agenthub wsl stop                            停止 WSL
  agenthub wsl restart                         重启 WSL
  agenthub status                              查看运行状态
  agenthub update check                        检查最新版本
  agenthub update download                     检查并下载、校验最新版本
  agenthub version                             查看版本

说明：
  命令需要 AgentHub 正在运行。程序启动时会把控制端口与令牌写入数据目录的
  auth.json，命令行读取后与应用通信。

  「开机时自动启动 WSL」是独立的开机行为设置，与 wsl start/stop 手动启停
  互不影响，也不依赖应用自身的开机自启设置。
`

var cliCommands = map[string]bool{
	"process":   true,
	"config":    true,
	"wsl":       true,
	"status":    true,
	"update":    true,
	"version":   true,
	"help":      true,
	"-h":        true,
	"--help":    true,
	"-v":        true,
	"--version": true,
}

// IsCommand reports whether args begin with a recognized CLI command. When it
// returns false the caller should start the GUI as usual.
func IsCommand(args []string) bool {
	return len(args) > 0 && cliCommands[args[0]]
}

// Run executes a CLI command and returns a process exit code.
func Run(args []string, opts Options) int {
	if len(args) == 0 {
		fmt.Fprint(opts.Stderr, usageText)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(opts.Stdout, usageText)
		return 0
	case "version", "-v", "--version":
		fmt.Fprintf(opts.Stdout, "AgentHub %s\n", opts.Version)
		return 0
	case "update":
		// Update commands talk to the release backend directly and must work
		// even when the GUI is not running.
		return runUpdate(args[1:], opts)
	}

	client, err := control.NewClient(opts.DataDir)
	if err != nil {
		fmt.Fprintln(opts.Stderr, connectError(err, opts.DataDir))
		return 1
	}

	var cmdErr error
	switch args[0] {
	case "status":
		cmdErr = runStatus(client, opts)
	case "process":
		cmdErr = runProcess(client, args[1:], opts)
	case "config":
		cmdErr = runConfig(client, args[1:], opts)
	case "wsl":
		cmdErr = runWSL(client, args[1:], opts)
	default:
		fmt.Fprint(opts.Stderr, usageText)
		return 2
	}
	if cmdErr != nil {
		fmt.Fprintln(opts.Stderr, "错误："+cmdErr.Error())
		return 1
	}
	return 0
}

func connectError(err error, dataDir string) string {
	if os.IsNotExist(err) {
		return fmt.Sprintf("AgentHub 未运行（未找到 %s），请先启动 AgentHub", control.AuthPath(dataDir))
	}
	return fmt.Sprintf("无法连接 AgentHub：%v", err)
}
