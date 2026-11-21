package scheduler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"service-health-checker/internal/model"
	"service-health-checker/internal/probe"
)

// recordingLogger 记录 Executor 抛出的事件序列，用于验证日志时机。
type recordingLogger struct {
	mu     sync.Mutex
	events []string
}

func (r *recordingLogger) add(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordingLogger) Running(target model.Target) { r.add("running:" + target.Name) }

func (r *recordingLogger) Retry(target model.Target, attempt, maxAttempts int, _ error) {
	r.add(fmt.Sprintf("retry:%s:%d/%d", target.Name, attempt, maxAttempts))
}

func (r *recordingLogger) Success(result model.ProbeResult) { r.add("success:" + result.Name) }

func (r *recordingLogger) Failed(result model.ProbeResult) { r.add("failed:" + result.Name) }

func (r *recordingLogger) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, len(r.events))
	copy(out, r.events)
	return out
}

func assertEvents(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("事件数量不符\n实际: %v\n期望: %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个事件不符\n实际: %v\n期望: %v", i+1, got, want)
		}
	}
}

func TestExecutorEventsRetryThenSuccess(t *testing.T) {
	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return probe.Metadata{}, errFake("first failure")
		}
		return probe.Metadata{StatusCode: 200}, nil
	})

	recorder := &recordingLogger{}
	executor := newTestExecutor(t, p, time.Second, 0).WithLogger(recorder)
	executor.Execute(context.Background(), httpTarget(1))

	assertEvents(t, recorder.snapshot(), []string{
		"running:test",
		"retry:test:1/2",
		"success:test",
	})
}

func TestExecutorEventsAllAttemptsFail(t *testing.T) {
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		return probe.Metadata{}, errFake("always fails")
	})

	recorder := &recordingLogger{}
	executor := newTestExecutor(t, p, time.Second, 0).WithLogger(recorder)
	executor.Execute(context.Background(), httpTarget(1))

	// 最后一次尝试失败后不应再输出 RETRY（没有下一次了）。
	assertEvents(t, recorder.snapshot(), []string{
		"running:test",
		"retry:test:1/2",
		"failed:test",
	})
}

func TestExecutorEventsCanceledDuringDelay(t *testing.T) {
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		t.Error("取消后不应发起探测")
		return probe.Metadata{}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	recorder := &recordingLogger{}
	executor := newTestExecutor(t, p, time.Second, 120*time.Millisecond).WithLogger(recorder)
	executor.Execute(ctx, httpTarget(0))

	assertEvents(t, recorder.snapshot(), []string{
		"running:test",
		"failed:test",
	})
}

func TestExecutorEventsUnsupportedProtocol(t *testing.T) {
	registry := probe.NewRegistry()
	recorder := &recordingLogger{}
	executor := NewExecutorWithDelay(registry, time.Second, 0).WithLogger(recorder)

	executor.Execute(context.Background(), model.Target{
		Name: "MySQL", Type: model.TypeTCP, Address: "localhost:3306",
	})

	assertEvents(t, recorder.snapshot(), []string{
		"running:MySQL",
		"failed:MySQL",
	})
}

// TestWithLoggerReturnsCopy 验证 WithLogger 不会修改原执行器实例。
func TestWithLoggerReturnsCopy(t *testing.T) {
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		return probe.Metadata{StatusCode: 200}, nil
	})

	original := newTestExecutor(t, p, time.Second, 0)
	recorder := &recordingLogger{}
	withLogger := original.WithLogger(recorder)

	original.Execute(context.Background(), httpTarget(0))
	if events := recorder.snapshot(); len(events) != 0 {
		t.Errorf("原执行器不应输出日志，实际事件: %v", events)
	}

	withLogger.Execute(context.Background(), httpTarget(0))
	if events := recorder.snapshot(); len(events) == 0 {
		t.Error("注入日志的执行器应输出事件")
	}
}
