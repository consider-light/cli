package model

import "time"

// ProbeResult 是一个探测目标经过全部重试后的最终结果。
//
// 约束（doc 6.2）：
//   - 每个 Target 最终只产生一条 ProbeResult，即使内部执行了多次尝试；
//   - ResponseTime 只记录"最后一次实际探测"的耗时，不含人工延迟，也不是多次
//     尝试耗时之和；
//   - Attempts 是真实执行次数，不是配置中的 RetryCount。
type ProbeResult struct {
	// Name 服务名称，来自配置。
	Name string
	// Type 协议类型，取值为 TypeHTTP 或 TypeTCP。
	Type string
	// Address 探测地址，来自配置。
	Address string
	// Success 是否为最终成功。只有某次尝试成功才为 true。
	Success bool
	// StatusCode HTTP 最终状态码；状态码不匹配时也保留实际收到的值。
	// TCP 探测不使用该字段，固定为 0。
	StatusCode int
	// ResponseTime 最后一次实际网络探测的耗时，不含人工延迟与重试间隔。
	ResponseTime time.Duration
	// Attempts 实际执行的探测次数（含首次）。若因取消未执行任何探测，则为 0。
	Attempts int
	// Error 最终失败原因；成功时为空字符串。
	Error string
}
