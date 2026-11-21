package scheduler

import (
	"context"
	"sync"

	"service-health-checker/internal/model"
)

// Scheduler 按"一个目标一个 Goroutine"的方式并发执行探测任务。
//
// 刻意不使用固定大小 Worker Pool：配置中有 N 个目标就启动 N 个 Goroutine，
// 所有任务尽可能同时开始（doc 9.1）。
type Scheduler struct {
	executor *Executor
}

// NewScheduler 使用给定执行器创建调度器。
func NewScheduler(executor *Executor) *Scheduler {
	return &Scheduler{executor: executor}
}

// Start 启动全部目标并返回只读的结果 Channel。
//
// Channel 的生命周期约定（doc 9.2 / 9.5）：
//   - 容量等于目标数量，因此每个任务发送结果时都不会阻塞，即使调用方读得较慢；
//   - 只有本函数启动的收尾 Goroutine 关闭 Channel，任务 Goroutine 绝不关闭；
//   - 收尾 Goroutine 在 WaitGroup 归零后才 close，保证"所有发送者已退出"。
//
// 调用方必须把返回的 Channel 读到底（例如交给 collector.Collect），否则任务的
// 结果将无人消费。
func (s *Scheduler) Start(ctx context.Context, targets []model.Target) <-chan model.ProbeResult {
	results := make(chan model.ProbeResult, len(targets))

	var wg sync.WaitGroup

	for _, target := range targets {
		// Add 必须在启动 Goroutine 之前调用，否则 Wait 可能先于 Add 返回。
		wg.Add(1)

		// 把循环变量作为参数传入，确保每个 Goroutine 持有自己的 Target 副本，
		// 不依赖循环变量的捕获语义。
		go func(t model.Target) {
			defer wg.Done()

			// 每个目标只发送一条最终结果；Execute 保证一定返回结果。
			results <- s.executor.Execute(ctx, t)
		}(target)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	return results
}
