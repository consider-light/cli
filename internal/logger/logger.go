// Package logger 提供并发安全的运行日志输出。
//
// 日志分三层（doc 11）：
//   - 普通运行信息（Infof）：无论是否 -v 都输出；
//   - 详细任务日志（Running/Retry/Success/Failed）：仅在 -v 开启时输出；
//   - 最终报告不属于本包，由 reporter 直接写终端。
//
// 所有输出都在同一把互斥锁的保护下"拼好整行再一次性写出"，因此多个 Goroutine
// 并发写日志时不会出现单行被穿插的情况。日志失败不影响业务结果：写错误被忽略。
package logger

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"service-health-checker/internal/model"
)

// Logger 是并发安全的日志输出器。零值不可用，请使用 New 创建。
type Logger struct {
	mu      sync.Mutex
	out     io.Writer
	verbose bool
}

// New 创建日志输出器。verbose 为 false 时只输出普通运行信息。
func New(out io.Writer, verbose bool) *Logger {
	return &Logger{out: out, verbose: verbose}
}

// Verbose 报告是否开启了详细模式。
func (l *Logger) Verbose() bool {
	return l.verbose
}

// Infof 输出一条普通运行信息，受 -v 影响为否（始终输出）。
func (l *Logger) Infof(format string, args ...any) {
	l.write("INFO", fmt.Sprintf(format, args...))
}

// Running 输出目标开始执行的信息，仅详细模式可见。
func (l *Logger) Running(target model.Target) {
	if !l.verbose {
		return
	}
	l.write("RUNNING", fmt.Sprintf("%s | %s | %s",
		target.Name, strings.ToUpper(target.Type), target.Address))
}

// Retry 输出一次失败后即将重试的信息，仅详细模式可见。
//
// attempt 是刚刚失败的尝试序号，maxAttempts 是本目标的最大执行次数，
// 因此括号内展示的是"下一次尝试 / 总次数"。
func (l *Logger) Retry(target model.Target, attempt, maxAttempts int, err error) {
	if !l.verbose {
		return
	}
	l.write("RETRY", fmt.Sprintf("%s | attempt %d failed, retrying (%d/%d) | %v",
		target.Name, attempt, attempt+1, maxAttempts, err))
}

// Success 输出目标最终成功的信息，仅详细模式可见。
func (l *Logger) Success(result model.ProbeResult) {
	if !l.verbose {
		return
	}
	l.write("SUCCESS", fmt.Sprintf("%s | %dms | attempts=%d",
		result.Name, result.ResponseTime.Milliseconds(), result.Attempts))
}

// Failed 输出目标最终失败的信息，仅详细模式可见。
func (l *Logger) Failed(result model.ProbeResult) {
	if !l.verbose {
		return
	}
	reason := result.Error
	if reason == "" {
		reason = "未知错误"
	}
	l.write("FAILED", fmt.Sprintf("%s | %s | attempts=%d", result.Name, reason, result.Attempts))
}

// write 在锁内完成整行拼装与写出，保证单条日志不被其他 Goroutine 穿插。
func (l *Logger) write(tag, message string) {
	line := "[" + tag + "] " + message + "\n"

	l.mu.Lock()
	defer l.mu.Unlock()
	// 日志写入失败不应影响探测结果，因此忽略返回的错误。
	_, _ = io.WriteString(l.out, line)
}
