// Package entry 把"命令行参数 → 退出码"的启动逻辑集中在一处。
//
// 根目录与 cmd/health-checker 各有一个 main，两者都调用本包，保证
// `go run .` 与 `go run ./cmd/health-checker` 的行为完全一致：
// 解析参数、安装 Ctrl+C 处理、执行探测、交出退出码。
package entry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"service-health-checker/internal/app"
	"service-health-checker/internal/cli"
)

// Run 执行一次完整启动流程并返回进程退出码。
//
// 参数解析失败、请求帮助、正常执行都通过返回值表达退出码，不在这里调用 os.Exit，
// 以便直接对函数做单元测试。
func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := cli.Parse(args)
	if err != nil {
		if errors.Is(err, cli.ErrHelp) {
			cli.Usage(stdout)
			return app.ExitOK
		}

		fmt.Fprintf(stderr, "[ERROR] %v\n\n", err)
		cli.Usage(stderr)
		return app.ExitStartupError
	}

	// 收到 Ctrl+C 时取消父 Context：正在执行的网络操作尽快结束，尚未开始的尝试
	// 不再发起，已取消的目标仍会返回一条结果（doc 12.2）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return app.New(*opts, stdout, stderr).Run(ctx)
}
