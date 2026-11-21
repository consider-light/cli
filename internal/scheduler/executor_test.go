package scheduler

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"service-health-checker/internal/model"
	"service-health-checker/internal/probe"
)

// funcProber 让测试用闭包直接描述"一次探测的行为"，避免为每个场景定义新类型。
type funcProber func(ctx context.Context, target model.Target) (probe.Metadata, error)

func (f funcProber) Probe(ctx context.Context, target model.Target) (probe.Metadata, error) {
	return f(ctx, target)
}

// newTestExecutor 构造一个只注册了 http Mock 探测器的执行器。
func newTestExecutor(t *testing.T, p probe.Prober, timeout, delay time.Duration) *Executor {
	t.Helper()

	registry := probe.NewRegistry()
	registry.MustRegister(model.TypeHTTP, p)
	return NewExecutorWithDelay(registry, timeout, delay)
}

// httpTarget 构造 HTTP 测试目标，retry 为首次失败后的额外重试次数。
func httpTarget(retry int) model.Target {
	return model.Target{
		Name:           "test",
		Type:           model.TypeHTTP,
		Address:        "http://example.test/health",
		RetryCount:     retry,
		ExpectedStatus: 200,
	}
}

func TestExecuteSuccessOnFirstAttempt(t *testing.T) {
	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		atomic.AddInt32(&calls, 1)
		return probe.Metadata{StatusCode: 200}, nil
	})

	start := time.Now()
	result := newTestExecutor(t, p, time.Second, 0).Execute(context.Background(), httpTarget(3))
	total := time.Since(start)

	if !result.Success {
		t.Fatalf("期望成功，实际错误: %s", result.Error)
	}
	if result.Attempts != 1 {
		t.Errorf("首次成功时 Attempts 应为 1，实际为 %d", result.Attempts)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("成功后不应继续重试，实际调用次数为 %d", got)
	}
	if result.StatusCode != 200 {
		t.Errorf("StatusCode 应为 200，实际为 %d", result.StatusCode)
	}
	// 单次探测耗时不应超过整个执行的耗时。
	// 注意：Windows 时钟粒度较粗，极快的 Mock 探测可能测得 0，因此不断言 > 0。
	if result.ResponseTime < 0 || result.ResponseTime > total {
		t.Errorf("ResponseTime 应在 [0, %v] 范围内，实际为 %v", total, result.ResponseTime)
	}
	if result.Error != "" {
		t.Errorf("成功时 Error 应为空，实际为 %q", result.Error)
	}
	if result.Name != "test" || result.Type != model.TypeHTTP || result.Address != "http://example.test/health" {
		t.Errorf("结果中的目标信息不完整: %#v", result)
	}
}

func TestExecuteRetryThenSuccess(t *testing.T) {
	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return probe.Metadata{StatusCode: 503}, errFake("temporary failure")
		}
		return probe.Metadata{StatusCode: 201}, nil
	})

	result := newTestExecutor(t, p, time.Second, 0).Execute(context.Background(), httpTarget(1))

	if !result.Success {
		t.Fatalf("第二次尝试成功，最终结果应为成功，实际错误: %s", result.Error)
	}
	if result.Attempts != 2 {
		t.Errorf("Attempts 应为 2，实际为 %d", result.Attempts)
	}
	if result.StatusCode != 201 {
		t.Errorf("StatusCode 应保留最后一次尝试的 201，实际为 %d", result.StatusCode)
	}
	if result.Error != "" {
		t.Errorf("最终成功时 Error 应为空，实际为 %q", result.Error)
	}
}

func TestExecuteAllAttemptsFail(t *testing.T) {
	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		atomic.AddInt32(&calls, 1)
		return probe.Metadata{}, errFake("connect refused")
	})

	result := newTestExecutor(t, p, time.Second, 0).Execute(context.Background(), httpTarget(2))

	if result.Success {
		t.Fatal("全部尝试失败时结果应为失败")
	}
	if result.Attempts != 3 {
		t.Errorf("RetryCount=2 时应执行 3 次，实际 Attempts 为 %d", result.Attempts)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("探测调用次数应为 3，实际为 %d", got)
	}
	if !strings.Contains(result.Error, "connect refused") {
		t.Errorf("Error 应保留最后一次失败原因，实际为 %q", result.Error)
	}
}

func TestExecuteZeroRetryCountMeansSingleAttempt(t *testing.T) {
	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		atomic.AddInt32(&calls, 1)
		return probe.Metadata{}, errFake("failed")
	})

	result := newTestExecutor(t, p, time.Second, 0).Execute(context.Background(), httpTarget(0))

	if result.Attempts != 1 {
		t.Errorf("RetryCount=0 时只应执行 1 次，实际为 %d", result.Attempts)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("探测调用次数应为 1，实际为 %d", got)
	}
}

// TestExecuteNegativeRetryCountIsClamped 验证上游传入非法负数时仍至少探测一次，
// 不会产生 Attempts=0 且 Error 为空的无声失败结果（配置层本应拦截该情况）。
func TestExecuteNegativeRetryCountIsClamped(t *testing.T) {
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		return probe.Metadata{}, errFake("failed")
	})

	result := newTestExecutor(t, p, time.Second, 0).Execute(context.Background(), httpTarget(-1))

	if result.Attempts != 1 {
		t.Errorf("RetryCount 为负数时应至少执行 1 次，实际 Attempts 为 %d", result.Attempts)
	}
	if result.Error == "" {
		t.Error("失败结果必须包含错误原因")
	}
}

// TestExecuteTimeoutIsPerAttempt 验证超时预算按每次尝试重新计算：
// 一次 80ms 超时的尝试失败后，下一次尝试会重新获得完整的 80ms，而不是共享总预算。
func TestExecuteTimeoutIsPerAttempt(t *testing.T) {
	const attemptTimeout = 80 * time.Millisecond

	p := funcProber(func(ctx context.Context, _ model.Target) (probe.Metadata, error) {
		// 模拟一个直到超时才返回的慢探测。
		<-ctx.Done()
		return probe.Metadata{}, ctx.Err()
	})

	start := time.Now()
	result := newTestExecutor(t, p, attemptTimeout, 0).Execute(context.Background(), httpTarget(1))
	elapsed := time.Since(start)

	if result.Attempts != 2 {
		t.Fatalf("应重试一次，Attempts 应为 2，实际为 %d", result.Attempts)
	}
	if result.Success {
		t.Fatal("两次尝试均超时，结果应为失败")
	}
	if elapsed < 120*time.Millisecond {
		t.Errorf("两次尝试应各自消耗约 80ms（合计约 160ms），实际仅 %v，"+
			"说明超时预算可能被所有尝试共享", elapsed)
	}
	if elapsed > time.Second {
		t.Errorf("超时控制未及时生效，实际耗时 %v", elapsed)
	}
	// ResponseTime 只记录最后一次尝试，而不是两次之和。
	if result.ResponseTime < 40*time.Millisecond || result.ResponseTime > 140*time.Millisecond {
		t.Errorf("ResponseTime 应约为单次 80ms，实际为 %v", result.ResponseTime)
	}
}

// TestExecuteStopsRetryingWhenParentCanceled 验证父 Context 取消后不再发起新的重试。
func TestExecuteStopsRetryingWhenParentCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		atomic.AddInt32(&calls, 1)
		cancel() // 模拟程序在第一次探测期间收到中断
		return probe.Metadata{}, errFake("interrupted")
	})

	result := newTestExecutor(t, p, time.Second, 0).Execute(ctx, httpTarget(3))

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("父 Context 取消后不应继续重试，实际调用次数为 %d", got)
	}
	if result.Attempts != 1 {
		t.Errorf("Attempts 应为 1，实际为 %d", result.Attempts)
	}
	if !strings.Contains(result.Error, "取消") {
		t.Errorf("Error 应注明因取消而停止重试，实际为 %q", result.Error)
	}
}

// TestExecuteDelayOnlyOnce 验证人工延迟在重试前不重复执行。
func TestExecuteDelayOnlyOnce(t *testing.T) {
	const delay = 120 * time.Millisecond

	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		atomic.AddInt32(&calls, 1)
		return probe.Metadata{}, errFake("failed")
	})

	start := time.Now()
	result := newTestExecutor(t, p, time.Second, delay).Execute(context.Background(), httpTarget(2))
	elapsed := time.Since(start)

	if result.Attempts != 3 {
		t.Fatalf("Attempts 应为 3，实际为 %d", result.Attempts)
	}
	if elapsed >= 3*delay {
		t.Errorf("人工延迟应在重试前不再执行，实际耗时 %v（3 次延迟约需 %v）", elapsed, 3*delay)
	}
	if elapsed < delay {
		t.Errorf("至少应包含一次人工延迟 %v，实际耗时 %v", delay, elapsed)
	}
}

// TestExecuteDelayExcludedFromResponseTime 验证人工延迟不计入 ResponseTime。
func TestExecuteDelayExcludedFromResponseTime(t *testing.T) {
	const delay = 100 * time.Millisecond

	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		return probe.Metadata{StatusCode: 200}, nil
	})

	start := time.Now()
	result := newTestExecutor(t, p, time.Second, delay).Execute(context.Background(), httpTarget(0))
	elapsed := time.Since(start)

	if elapsed < delay {
		t.Errorf("整个执行应至少包含 %v 人工延迟，实际为 %v", delay, elapsed)
	}
	if result.ResponseTime >= delay/2 {
		t.Errorf("ResponseTime 不应包含人工延迟，实际为 %v", result.ResponseTime)
	}
}

// TestExecuteUnsupportedProtocol 验证协议不支持时返回失败结果且不进入重试流程。
func TestExecuteUnsupportedProtocol(t *testing.T) {
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		t.Error("协议不受支持时不应调用探测器")
		return probe.Metadata{}, nil
	})

	// 注册表只注册了 http，这里刻意使用 tcp 目标。
	registry := probe.NewRegistry()
	registry.MustRegister(model.TypeHTTP, p)
	executor := NewExecutorWithDelay(registry, time.Second, 0)

	result := executor.Execute(context.Background(), model.Target{
		Name:       "MySQL",
		Type:       model.TypeTCP,
		Address:    "localhost:3306",
		RetryCount: 3,
	})

	if result.Success {
		t.Fatal("协议不受支持时结果应为失败")
	}
	if result.Attempts != 0 {
		t.Errorf("协议不受支持时不应执行探测，Attempts 应为 0，实际为 %d", result.Attempts)
	}
	if !strings.Contains(result.Error, "不支持的协议类型") {
		t.Errorf("Error 应说明协议不受支持，实际为 %q", result.Error)
	}
}

// TestExecuteCanceledDuringDelay 验证人工延迟期间被取消时不再发起网络探测。
func TestExecuteCanceledDuringDelay(t *testing.T) {
	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		atomic.AddInt32(&calls, 1)
		return probe.Metadata{}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	result := newTestExecutor(t, p, time.Second, 120*time.Millisecond).Execute(ctx, httpTarget(0))

	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("取消后不应发起网络探测，实际调用次数为 %d", got)
	}
	if result.Attempts != 0 {
		t.Errorf("Attempts 应为 0，实际为 %d", result.Attempts)
	}
	if !strings.Contains(result.Error, "取消") {
		t.Errorf("Error 应说明任务被取消，实际为 %q", result.Error)
	}
}

// errFake 是测试用的固定错误类型，便于断言错误文本。
type errFake string

func (e errFake) Error() string { return string(e) }
