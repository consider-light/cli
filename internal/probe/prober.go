// Package probe 定义统一的协议探测接口，并提供 HTTP、TCP 两种实现与探测器注册表。
//
// 探测器的职责被严格限制为"执行一次协议探测"：它不负责重试、不负责创建超时
// Context、也不负责日志与统计。超时预算由调用方（Executor）通过 ctx 传入，
// 因此每个探测器都必须把 ctx 传递到底层网络调用，并在 ctx 取消或超时后尽快返回。
package probe

import (
	"context"

	"service-health-checker/internal/model"
)

// Metadata 描述一次探测中获得的协议级附加信息。
type Metadata struct {
	// StatusCode HTTP 响应的最终状态码；TCP 探测不使用该字段，固定为 0。
	StatusCode int
}

// Prober 执行一次协议探测。
//
// 实现约定：
//   - 返回 nil error 表示本次探测成功，error 非 nil 表示本次探测失败；
//   - 失败时仍应尽量返回已获得的 Metadata（例如状态码不匹配时保留实际状态码，
//     便于结果模型记录）；
//   - 不实现重试：重试次数由 Executor 依据 RetryCount 控制；
//   - 必须响应 ctx 的取消与超时，不得自行使用 time.After 等方式伪造超时。
type Prober interface {
	Probe(ctx context.Context, target model.Target) (Metadata, error)
}
