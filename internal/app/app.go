// Package app 负责依赖组装与完整流程编排。
//
// 流程顺序与 doc 4.1 一致：加载配置 -> 校验协议 -> 启动调度 -> 收集结果 ->
// 输出报告 -> 由结果决定退出码。本包不实现 HTTP/TCP 细节，也不做统计计算。
package app

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"service-health-checker/internal/cli"
	"service-health-checker/internal/collector"
	"service-health-checker/internal/config"
	"service-health-checker/internal/logfile"
	"service-health-checker/internal/logger"
	"service-health-checker/internal/model"
	"service-health-checker/internal/probe"
	"service-health-checker/internal/reporter"
	"service-health-checker/internal/scheduler"
)

// 退出码约定（doc 3.4）。
const (
	// ExitOK 所有目标最终探测成功，或正常显示了帮助信息。
	ExitOK = 0
	// ExitTargetFailed 至少一个目标最终探测失败。
	ExitTargetFailed = 1
	// ExitStartupError 参数解析、配置读取或配置校验等启动阶段错误。
	ExitStartupError = 2
)

// DefaultLogDir 是日志文件的默认生成目录：进程的当前工作目录。
const DefaultLogDir = "."

// App 是一次运行的应用实例。
type App struct {
	opts     cli.Options
	registry *probe.Registry
	stdout   io.Writer
	stderr   io.Writer
	// logDir 是日志文件的生成目录；为空表示不生成日志文件。
	logDir string
}

// New 使用默认探测器注册表创建应用。
func New(opts cli.Options, stdout, stderr io.Writer) *App {
	return NewWithRegistry(opts, probe.DefaultRegistry(), stdout, stderr)
}

// NewWithRegistry 允许注入探测器注册表，便于测试与后续扩展。
func NewWithRegistry(opts cli.Options, registry *probe.Registry, stdout, stderr io.Writer) *App {
	return &App{
		opts:     opts,
		registry: registry,
		stdout:   stdout,
		stderr:   stderr,
		logDir:   DefaultLogDir,
	}
}

// WithLogDir 返回把日志文件生成目录改为 dir 的副本。
//
// 传空字符串表示本次运行不生成日志文件，供测试与"只想要终端输出"的场景使用。
// 返回副本而不是就地修改，避免同一个 App 被并发复用时互相影响。
func (a *App) WithLogDir(dir string) *App {
	clone := *a
	clone.logDir = dir
	return &clone
}

// Run 执行完整流程并返回进程退出码。
//
// 终端与日志文件写同样的内容：日志行与报告都经过同一个 io.Writer（stdout 与
// 日志文件的 MultiWriter），因此文件里看到的就是终端上看到的。日志文件只是附加
// 产物，创建失败时降级为"仅终端输出"并打印警告，不改变退出码。
func (a *App) Run(ctx context.Context) int {
	startedAt := time.Now()

	out := a.stdout
	errOut := a.stderr

	if a.logDir != "" {
		logFile, err := logfile.Create(a.logDir, startedAt)
		if err != nil {
			// 写不了日志文件不应该让探测失败：主功能是探测，不是记日志。
			fmt.Fprintf(a.stderr, "[WARN] %v，本次仅在终端输出\n", err)
		} else {
			defer func() { _ = logFile.Close() }()

			logFile.WriteHeader(startedAt, a.opts.ConfigPath, a.opts.Timeout)
			out = io.MultiWriter(a.stdout, logFile)
			errOut = io.MultiWriter(a.stderr, logFile)

			// 这行只写终端：文件里已经有头部交代过自己的路径，再写一遍会自我引用。
			fmt.Fprintf(a.stdout, "[INFO] 日志文件: %s\n", logFile.Path())
		}
	}

	log := logger.New(out, a.opts.Verbose)

	cfg, err := config.Load(a.opts.ConfigPath)
	if err != nil {
		fmt.Fprintf(errOut, "[ERROR] %v\n", err)
		return ExitStartupError
	}

	// 启动前预校验协议类型：协议不支持属于启动前错误，不进入重试流程（doc 2.4）。
	// 正常配置只可能是 http/tcp，这里用于在调度开始前就暴露注册表与配置不一致的问题。
	for _, target := range cfg.Targets {
		if _, err := a.registry.Get(target.Type); err != nil {
			fmt.Fprintf(errOut, "[ERROR] 目标 %q 的协议类型非法: %v\n", target.Name, err)
			return ExitStartupError
		}
	}

	log.Infof("Loaded %d targets from %s (timeout=%s)", len(cfg.Targets), cfg.Source, a.opts.Timeout)
	log.Infof("Starting service health checks...")

	executor := scheduler.NewExecutor(a.registry, a.opts.Timeout).WithLogger(log)
	start := time.Now()
	results := collector.Collect(scheduler.NewScheduler(executor).Start(ctx, cfg.Targets))
	elapsed := time.Since(start)

	log.Infof("All targets completed in %s", elapsed.Round(time.Millisecond))
	if ctx.Err() != nil {
		log.Infof("检测到中断信号，报告只包含已完成或已被取消的目标")
	}

	// 报告必须在全部结果收集完成后输出，同时写入日志文件（同一份内容）。
	// 结果先按配置顺序重排：并发完成顺序每次都不同，直接输出会让同一份配置
	// 产生顺序不同的报告，不利于对比与排查。
	reporter.Write(out, orderByConfig(results, cfg.Targets), elapsed)
	log.Infof("Report finished.")

	for _, result := range results {
		if !result.Success {
			return ExitTargetFailed
		}
	}
	return ExitOK
}

// orderByConfig 按配置中的目标顺序重排结果，使报告在同一份配置下保持稳定。
//
// 调度层按完成顺序产出结果，先返回的目标并不等于配置里靠前的目标；直接输出会
// 让每次运行的目标明细顺序都不同。这里以 name+type+address 作为身份键（不新增
// ProbeResult 字段，避免改动 doc 6.2 已确认的数据模型），未在配置中找到的结果
// 排在最后，保持它们之间的原有顺序（SliceStable）。
//
// 配置中完全相同的重复目标无法区分，会落到同一个位置，这是可接受的退化：
// 它们在报告中的字段本来就完全一致。
func orderByConfig(results []model.ProbeResult, targets []model.Target) []model.ProbeResult {
	if len(results) == 0 || len(targets) == 0 {
		return results
	}

	position := make(map[string]int, len(targets))
	for i, target := range targets {
		key := resultKey(target.Name, target.Type, target.Address)
		if _, exists := position[key]; !exists {
			position[key] = i
		}
	}

	ordered := make([]model.ProbeResult, len(results))
	copy(ordered, results)

	sort.SliceStable(ordered, func(i, j int) bool {
		pi, okI := position[resultKey(ordered[i].Name, ordered[i].Type, ordered[i].Address)]
		pj, okJ := position[resultKey(ordered[j].Name, ordered[j].Type, ordered[j].Address)]
		switch {
		case okI && okJ:
			return pi < pj
		case okI:
			return true
		case okJ:
			return false
		default:
			return false
		}
	})
	return ordered
}

// resultKey 拼接结果与配置目标的身份键，使用 NUL 分隔以避免字段内容相互拼接产生歧义。
func resultKey(name, typ, address string) string {
	return name + "\x00" + typ + "\x00" + address
}
