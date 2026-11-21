// Package scheduler 负责并发调度与单个目标的完整执行流程。
//
// 职责边界（doc 4.2）：
//   - Scheduler 只负责"一个目标一个 Goroutine"的启动与收尾、结果 Channel 的关闭；
//   - Executor 只负责单个目标的生命周期：人工延迟、取探测器、超时控制、重试、
//     生成最终结果。
//
// 两者都不实现协议请求细节，也不做统计与报告。
package scheduler

import (
	"context"
	"fmt"
	"time"

	"service-health-checker/internal/model"
	"service-health-checker/internal/probe"
)

// DefaultArtificialDelay 是项目要求的人为延迟。
//
// 每个目标在首次网络探测前固定等待 1 秒，用于展示"瞬间并发"的效果。
// 该延迟只执行一次，不计入 ProbeResult.ResponseTime，也不占用单次探测的超时预算
// （doc 2.5）。
const DefaultArtificialDelay = time.Second

// EventLogger 是 Executor 的运行日志出口。
//
// 接口定义在消费方（scheduler），由 app 注入具体实现，因此 scheduler 不需要依赖
// logger 包，依赖方向保持 doc 4.3 描述的单向结构。实现必须并发安全：多个目标的
// Goroutine 会同时调用这些方法。
type EventLogger interface {
	// Running 在目标开始执行时调用（人工延迟之前）。
	Running(target model.Target)
	// Retry 在一次尝试失败、即将进行下一次尝试时调用。
	Retry(target model.Target, attempt, maxAttempts int, err error)
	// Success 在目标最终成功时调用。
	Success(result model.ProbeResult)
	// Failed 在目标最终失败时调用，包含因取消或协议不支持而未执行探测的情况。
	Failed(result model.ProbeResult)
}

// nopEventLogger 是未注入日志时的默认实现，避免每次调用都做 nil 判断。
type nopEventLogger struct{}

func (nopEventLogger) Running(model.Target)                {}
func (nopEventLogger) Retry(model.Target, int, int, error) {}
func (nopEventLogger) Success(model.ProbeResult)           {}
func (nopEventLogger) Failed(model.ProbeResult)            {}

// Executor 执行单个目标的完整生命周期：人工延迟 -> 取探测器 -> 首次探测 -> 按需重试
// -> 生成唯一一条最终结果。
//
// Executor 本身不持有可变状态，可被多个目标 Goroutine 并发调用；每一次 Execute
// 的中间状态都保存在局部变量里，因此不存在跨目标的数据竞争。
type Executor struct {
	registry *probe.Registry
	timeout  time.Duration
	delay    time.Duration
	logger   EventLogger
}

// NewExecutor 创建正式运行使用的执行器：单次探测超时为 timeout，人工延迟固定 1 秒。
func NewExecutor(registry *probe.Registry, timeout time.Duration) *Executor {
	return &Executor{
		registry: registry,
		timeout:  timeout,
		delay:    DefaultArtificialDelay,
	}
}

// NewExecutorWithDelay 创建可自定义人工延迟的执行器。
//
// 仅供单元测试与性能测试缩短等待时间使用；正式运行路径必须使用 NewExecutor，
// 以保证 1 秒延迟的行为不被测试代码替换掉（doc 13.3）。delay 允许为 0。
func NewExecutorWithDelay(registry *probe.Registry, timeout, delay time.Duration) *Executor {
	return &Executor{
		registry: registry,
		timeout:  timeout,
		delay:    delay,
	}
}

// WithLogger 返回一个注入了日志出口的执行器副本，原实例保持不变。
//
// 返回副本而不是就地赋值，是为了避免"运行中替换 logger"造成的数据竞争；
// 因此必须在 Scheduler.Start 之前完成注入。logger 为 nil 时等价于不输出任务日志。
func (e *Executor) WithLogger(logger EventLogger) *Executor {
	clone := *e
	clone.logger = logger
	return &clone
}

// eventLogger 返回实际使用的日志实现，未注入时返回空实现。
func (e *Executor) eventLogger() EventLogger {
	if e.logger == nil {
		return nopEventLogger{}
	}
	return e.logger
}

// Execute 执行一个目标并返回其唯一一条最终结果。
//
// 关键语义（doc 9.3）：
//   - 人工延迟在首次探测前执行且仅执行一次，重试前不再重复等待；
//   - 每次尝试都创建独立的 timeout Context，超时预算重新计算；
//   - 每次尝试结束后立即 cancel，不把释放推迟到函数返回；
//   - 成功后立即结束，不再重试；
//   - 父 Context 取消后不再发起新的重试；
//   - 无论成功失败，都返回一条结果，绝不 panic、绝不返回零值占位。
func (e *Executor) Execute(ctx context.Context, target model.Target) model.ProbeResult {
	result := model.ProbeResult{
		Name:    target.Name,
		Type:    target.Type,
		Address: target.Address,
	}
	events := e.eventLogger()

	// 任务启动即输出，便于在 -v 模式下观察"所有目标几乎同时开始"。
	events.Running(target)

	// 人为延迟：项目要求的固定 1 秒（测试中可注入更短值）。
	// 使用 time.Sleep 而非 select 等待，行为与需求文档的字面要求一致；
	// 延迟结束后立即检查父 Context，避免在程序已中断的情况下继续发起网络请求。
	time.Sleep(e.delay)
	if err := ctx.Err(); err != nil {
		result.Error = fmt.Sprintf("任务在人工延迟期间被取消，未执行网络探测: %v", err)
		events.Failed(result)
		return result
	}

	// 协议类型不支持属于启动前错误，不进入重试流程（doc 2.4）。
	// 正常情况下配置层已拦截未知协议，这里是调度层的兜底：返回一条失败结果，
	// 而不是 panic 或让该目标的结果缺失。
	prober, err := e.registry.Get(target.Type)
	if err != nil {
		result.Error = err.Error()
		events.Failed(result)
		return result
	}

	// RetryCount 表示首次失败后的额外重试次数，因此最大执行次数为 RetryCount + 1。
	// 配置层已保证取值范围 0～3，这里做防御性收敛：即使上游传入负数，也至少执行
	// 一次，避免产生一条 Attempts=0 且 Error 为空的"无声失败"结果。
	maxAttempts := target.RetryCount + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// 每次尝试都必须重新创建 timeout Context，使超时预算独立（doc 9.4）。
		attemptCtx, cancel := context.WithTimeout(ctx, e.timeout)

		started := time.Now()
		metadata, probeErr := prober.Probe(attemptCtx, target)
		elapsed := time.Since(started)

		// 本次尝试结束，立即释放 Context 与内部定时器。
		cancel()

		// 记录真实执行次数与最后一次探测的状态码、耗时。
		result.Attempts = attempt
		result.ResponseTime = elapsed
		result.StatusCode = metadata.StatusCode

		if probeErr == nil {
			result.Success = true
			result.Error = ""
			events.Success(result)
			return result
		}

		result.Error = probeErr.Error()

		// 父 Context 已取消时不得继续重试；注意判断的是父 ctx 而不是本次
		// attemptCtx——单次尝试自己超时并不代表程序被中断，仍允许重试。
		if ctxErr := ctx.Err(); ctxErr != nil {
			result.Error = fmt.Sprintf("%s（父上下文已取消，跳过剩余重试）", result.Error)
			events.Failed(result)
			return result
		}

		// 仅当确实还有下一次尝试时才输出 RETRY，避免最后一条尝试也报"即将重试"。
		if attempt < maxAttempts {
			events.Retry(target, attempt, maxAttempts, probeErr)
		}
	}

	events.Failed(result)
	return result
}
