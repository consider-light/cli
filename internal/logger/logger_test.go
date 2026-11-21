package logger

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"service-health-checker/internal/model"
)

func TestInfofAlwaysOutputs(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		var buf bytes.Buffer
		New(&buf, verbose).Infof("加载 %d 个目标", 3)

		want := "[INFO] 加载 3 个目标\n"
		if buf.String() != want {
			t.Errorf("verbose=%t 时输出应为 %q，实际为 %q", verbose, want, buf.String())
		}
	}
}

// TestTaskLogsRequireVerbose 验证任务日志只在 -v 时输出（doc 3.3 / 11）。
func TestTaskLogsRequireVerbose(t *testing.T) {
	target := model.Target{Name: "百度搜索", Type: model.TypeHTTP, Address: "https://www.baidu.com"}
	result := model.ProbeResult{Name: "百度搜索", Success: true, ResponseTime: 5 * time.Millisecond, Attempts: 1}

	var quiet bytes.Buffer
	logger := New(&quiet, false)
	logger.Running(target)
	logger.Retry(target, 1, 2, errors.New("timeout"))
	logger.Success(result)
	logger.Failed(model.ProbeResult{Name: "x", Error: "boom", Attempts: 1})

	if quiet.Len() != 0 {
		t.Errorf("非详细模式不应输出任务日志，实际输出:\n%s", quiet.String())
	}
}

func TestEventFormats(t *testing.T) {
	target := model.Target{Name: "百度搜索", Type: model.TypeHTTP, Address: "https://www.baidu.com"}

	tests := []struct {
		name string
		emit func(l *Logger)
		want string
	}{
		{
			name: "RUNNING",
			emit: func(l *Logger) { l.Running(target) },
			want: "[RUNNING] 百度搜索 | HTTP | https://www.baidu.com\n",
		},
		{
			name: "RETRY",
			emit: func(l *Logger) { l.Retry(target, 1, 2, errors.New("i/o timeout")) },
			want: "[RETRY] 百度搜索 | attempt 1 failed, retrying (2/2) | i/o timeout\n",
		},
		{
			name: "SUCCESS",
			emit: func(l *Logger) {
				l.Success(model.ProbeResult{Name: "MySQL服务", ResponseTime: 5 * time.Millisecond, Attempts: 1})
			},
			want: "[SUCCESS] MySQL服务 | 5ms | attempts=1\n",
		},
		{
			name: "FAILED",
			emit: func(l *Logger) {
				l.Failed(model.ProbeResult{Name: "无效服务", Error: "DNS lookup failed", Attempts: 2})
			},
			want: "[FAILED] 无效服务 | DNS lookup failed | attempts=2\n",
		},
		{
			name: "FAILED 无错误文本",
			emit: func(l *Logger) {
				l.Failed(model.ProbeResult{Name: "未知", Attempts: 0})
			},
			want: "[FAILED] 未知 | 未知错误 | attempts=0\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.emit(New(&buf, true))

			if buf.String() != tc.want {
				t.Errorf("输出不符合预期\n实际: %q\n期望: %q", buf.String(), tc.want)
			}
		})
	}
}

// TestConcurrentWritesKeepLinesIntact 验证多 Goroutine 并发写日志时单行不会被穿插。
func TestConcurrentWritesKeepLinesIntact(t *testing.T) {
	const (
		goroutines = 40
		perRoutine = 25
	)

	var buf bytes.Buffer
	// bytes.Buffer 本身不并发安全，必须由 Logger 的互斥锁保证串行写入；
	// 若实现缺少锁，-race 会直接报错，同时下面的行完整性检查也会失败。
	logger := New(&buf, true)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perRoutine; j++ {
				logger.Infof("worker %d line %d 中文内容", id, j)
			}
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != goroutines*perRoutine {
		t.Fatalf("应有 %d 行日志，实际为 %d 行", goroutines*perRoutine, len(lines))
	}

	pattern := regexp.MustCompile(`^\[INFO\] worker \d+ line \d+ 中文内容$`)
	for _, line := range lines {
		if !pattern.MatchString(line) {
			t.Fatalf("发现被穿插或残缺的日志行: %q", line)
		}
	}
}

func TestVerboseGetter(t *testing.T) {
	if New(&bytes.Buffer{}, false).Verbose() {
		t.Error("Verbose() 应返回 false")
	}
	if !New(&bytes.Buffer{}, true).Verbose() {
		t.Error("Verbose() 应返回 true")
	}
}

// failingWriter 用于验证写日志失败不会 panic。
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("write failed")
}

func TestWriteErrorDoesNotPanic(t *testing.T) {
	logger := New(failingWriter{}, true)
	logger.Infof("这条日志写入会失败")
	logger.Running(model.Target{Name: "x", Type: model.TypeHTTP, Address: "http://example.test"})
}
