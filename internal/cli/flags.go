// Package cli 负责命令行参数的定义与解析。
//
// 该包只做"把 []string 变成运行选项"，不读取配置文件、不执行探测、不决定退出码；
// 参数不合法时返回错误，由 cmd 层打印提示并决定退出码（doc 3.1 / 3.4）。
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"service-health-checker/internal/config"
)

// DefaultTimeoutSeconds 是 --timeout 的默认值，单位为秒（doc 3.1）。
const DefaultTimeoutSeconds = 3

// ErrHelp 表示用户显式请求帮助信息（-h / --help）。
// 调用方应打印用法并以退出码 0 结束，而不是当作错误处理。
var ErrHelp = errors.New("请求显示帮助信息")

// Options 是一次运行的全部命令行选项。
type Options struct {
	// ConfigPath JSON 配置文件路径，相对于进程的当前工作目录。
	ConfigPath string
	// Timeout 单次网络探测的超时时间，由 --timeout 的秒数转换而来。
	Timeout time.Duration
	// Verbose 是否开启详细模式。
	Verbose bool
}

// Parse 解析命令行参数（不包含程序名）。
//
// 支持 --config、--timeout、-v 三个参数，flag 包同时兼容单横线写法
// （-config、-timeout）。解析失败、参数值非法、出现多余的位置参数都会返回错误，
// 并在错误信息中说明具体原因。
func Parse(args []string) (*Options, error) {
	fs := flag.NewFlagSet("health-checker", flag.ContinueOnError)
	// 由调用方决定何时、向哪个流打印用法，这里抑制 flag 的自动输出。
	fs.SetOutput(io.Discard)

	configPath := fs.String("config", config.DefaultPath, "JSON 配置文件路径")
	timeoutSeconds := fs.Int("timeout", DefaultTimeoutSeconds, "单次网络探测超时时间（秒）")
	verbose := fs.Bool("v", false, "开启详细模式")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, ErrHelp
		}
		return nil, fmt.Errorf("参数解析失败: %w", err)
	}

	// 本工具没有位置参数，多出来的内容通常是拼写错误，直接报错比静默忽略更安全。
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("存在无法识别的参数: %s", strings.Join(fs.Args(), " "))
	}

	if *timeoutSeconds <= 0 {
		return nil, fmt.Errorf("--timeout 必须大于 0，当前为 %d", *timeoutSeconds)
	}

	path := strings.TrimSpace(*configPath)
	if path == "" {
		return nil, errors.New("--config 不能为空")
	}

	return &Options{
		ConfigPath: path,
		Timeout:    time.Duration(*timeoutSeconds) * time.Second,
		Verbose:    *verbose,
	}, nil
}

// Usage 向 w 输出用法说明。
func Usage(w io.Writer) {
	fmt.Fprintf(w, `Service Health Checker - 并发服务健康探测 CLI

用法:
  health-checker [选项]

选项:
  --config string   JSON 配置文件路径（默认 %s，相对于当前工作目录）
  --timeout int     单次网络探测超时时间，单位秒（默认 %d，必须大于 0）
  -v                开启详细模式，实时输出目标启动、重试与最终结果

说明:
  每个目标对应一个 Goroutine 并发执行；单次探测超时由 --timeout 控制，
  重试次数由配置中的 retry_count 控制（首次失败后的额外重试次数，0～3）。

示例:
  go run ./cmd/health-checker
  go run ./cmd/health-checker --config ./config.json
  go run ./cmd/health-checker --timeout 5 -v
`, config.DefaultPath, DefaultTimeoutSeconds)
}
