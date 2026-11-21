package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"service-health-checker/internal/model"
	"service-health-checker/internal/probe"
)

// instantProber 立即返回成功，用于隔离调度行为本身。
func instantProber() probe.Prober {
	return funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		return probe.Metadata{StatusCode: 200}, nil
	})
}

// newTestScheduler 构造一个使用指定 Mock 探测器与人工延迟的调度器。
func newTestScheduler(t *testing.T, p probe.Prober, delay time.Duration) *Scheduler {
	t.Helper()

	registry := probe.NewRegistry()
	registry.MustRegister(model.TypeHTTP, p)
	return NewScheduler(NewExecutorWithDelay(registry, time.Second, delay))
}

// httpTargets 构造 count 个名称唯一的 HTTP 目标。
func httpTargets(count int) []model.Target {
	targets := make([]model.Target, 0, count)
	for i := 0; i < count; i++ {
		targets = append(targets, model.Target{
			Name:           "target-" + string(rune('a'+i)),
			Type:           model.TypeHTTP,
			Address:        "http://example.test",
			ExpectedStatus: 200,
		})
	}
	return targets
}

// TestStartCollectsOneResultPerTarget 验证 N 个目标产生 N 条结果，且 Channel 最终关闭。
func TestStartCollectsOneResultPerTarget(t *testing.T) {
	const count = 5
	s := newTestScheduler(t, instantProber(), 0)

	ch := s.Start(context.Background(), httpTargets(count))

	seen := make(map[string]int, count)
	for result := range ch {
		seen[result.Name]++
	}

	if len(seen) != count {
		t.Errorf("应收到 %d 个不同目标的结果，实际为 %d", count, len(seen))
	}
	for name, times := range seen {
		if times != 1 {
			t.Errorf("目标 %q 应只产生一条结果，实际为 %d 条", name, times)
		}
	}

	// range 结束后再读一次，确认 Channel 处于关闭状态（而不是还剩数据）。
	if _, ok := <-ch; ok {
		t.Error("Channel 应在全部结果发送完毕后关闭")
	}
}

// TestStartRunsTargetsConcurrently 是需求要求的并发性能测试（doc 13.3）。
//
// 10 个目标、每个都有 1 秒人工延迟：串行执行约需 10 秒，并发执行应接近 1 秒。
// 阈值留了充足余量，因为机器负载会影响实际耗时。
func TestStartRunsTargetsConcurrently(t *testing.T) {
	const (
		count         = 10
		serialCost    = count * time.Second
		elapsedBudget = 3 * time.Second
	)

	registry := probe.NewRegistry()
	registry.MustRegister(model.TypeHTTP, instantProber())

	// 使用正式构造函数，确保测试覆盖真实的 1 秒人工延迟。
	s := NewScheduler(NewExecutor(registry, 5*time.Second))

	start := time.Now()
	results := 0
	for range s.Start(context.Background(), httpTargets(count)) {
		results++
	}
	elapsed := time.Since(start)

	if results != count {
		t.Fatalf("应收集到 %d 条结果，实际为 %d", count, results)
	}
	if elapsed >= serialCost {
		t.Errorf("并发执行耗时 %v，未明显优于串行执行的约 %v", elapsed, serialCost)
	}
	if elapsed > elapsedBudget {
		t.Errorf("并发执行耗时 %v，超出预期阈值 %v（机器负载可能影响结果）", elapsed, elapsedBudget)
	}
	if elapsed < time.Second {
		t.Errorf("耗时 %v 小于一次人工延迟，说明 1 秒延迟可能未生效", elapsed)
	}
}

// TestStartEmptyTargets 验证空目标列表时 Channel 立即关闭，Collector 不会永久阻塞。
func TestStartEmptyTargets(t *testing.T) {
	s := newTestScheduler(t, instantProber(), 0)

	ch := s.Start(context.Background(), nil)

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("空目标列表不应产生任何结果")
		}
	case <-time.After(time.Second):
		t.Fatal("空目标列表时 Channel 应立即关闭")
	}
}

// TestStartIsolatesFailures 验证单个目标失败不会中断其他目标。
func TestStartIsolatesFailures(t *testing.T) {
	p := funcProber(func(_ context.Context, target model.Target) (probe.Metadata, error) {
		if target.Name == "bad" {
			return probe.Metadata{}, errFake("boom")
		}
		return probe.Metadata{StatusCode: 200}, nil
	})

	s := newTestScheduler(t, p, 0)
	targets := []model.Target{
		{Name: "ok-1", Type: model.TypeHTTP, Address: "http://example.test", ExpectedStatus: 200},
		{Name: "bad", Type: model.TypeHTTP, Address: "http://example.test", ExpectedStatus: 200},
		{Name: "ok-2", Type: model.TypeHTTP, Address: "http://example.test", ExpectedStatus: 200},
	}

	succeeded, failed := 0, 0
	for result := range s.Start(context.Background(), targets) {
		if result.Success {
			succeeded++
		} else {
			failed++
		}
	}

	if succeeded != 2 || failed != 1 {
		t.Errorf("应为 2 成功 1 失败，实际为 %d 成功 %d 失败", succeeded, failed)
	}
}

// TestStartRespectsCancelation 验证父 Context 取消后所有目标仍会返回结果，不会丢结果也不会阻塞。
func TestStartRespectsCancelation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls int32
	p := funcProber(func(context.Context, model.Target) (probe.Metadata, error) {
		atomic.AddInt32(&calls, 1)
		return probe.Metadata{}, nil
	})

	s := newTestScheduler(t, p, 0)

	count := 0
	for range s.Start(ctx, httpTargets(3)) {
		count++
	}

	if count != 3 {
		t.Errorf("取消后仍应为每个目标返回一条结果，实际为 %d 条", count)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("父 Context 已取消时不应发起探测，实际调用 %d 次", got)
	}
}
