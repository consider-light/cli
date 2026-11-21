package reporter

import (
	"math"
	"sort"
	"time"

	"service-health-checker/internal/model"
)

// LatencyStats 描述成功目标的响应时间统计。
//
// 口径（doc 10.2）：只统计最终 Success == true 的目标；失败目标不参与延迟分布，
// 避免 DNS 失败、超时等异常数据扭曲正常服务的延迟分布。
// Samples 为 0 时其余字段均无意义，报告层应显示 N/A。
type LatencyStats struct {
	Samples int
	Min     time.Duration
	Max     time.Duration
	Avg     time.Duration
	P50     time.Duration
	P95     time.Duration
}

// Bucket 是响应时间分布中的一个左闭右开区间。
type Bucket struct {
	// Label 区间展示文本，例如 "[100ms, 500ms)"。
	Label string
	// Count 落入该区间的成功目标数量。
	Count int
}

// Statistics 是一次运行的完整统计结果。
type Statistics struct {
	// Total 配置中参与本次运行的目标总数。
	Total int
	// Succeeded 最终成功的目标数量。
	Succeeded int
	// Failed 最终失败的目标数量。
	Failed int
	// TotalDuration 从启动调度到收集完全部结果的墙钟时间。
	TotalDuration time.Duration
	// Latency 成功目标的响应时间统计。
	Latency LatencyStats
	// Distribution 成功目标的响应时间分布，各区间数量之和等于 Succeeded。
	Distribution []Bucket
	// Slowest 最慢的成功目标，按 ResponseTime 降序，最多 3 条。
	Slowest []model.ProbeResult
}

// SuccessRate 返回成功率百分比（成功数 / 总数 × 100）。
// 总数为 0 时返回 0，避免除以 0。
func (s Statistics) SuccessRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Succeeded) / float64(s.Total) * 100
}

// bucketUpper 描述分布区间的右边界；非正数表示 +inf。
type bucketUpper struct {
	label string
	upper time.Duration
}

// bucketUppers 是 doc 10.3 规定的互不重叠区间，顺序即匹配优先级。
var bucketUppers = []bucketUpper{
	{label: "[0ms, 100ms)", upper: 100 * time.Millisecond},
	{label: "[100ms, 500ms)", upper: 500 * time.Millisecond},
	{label: "[500ms, 1000ms)", upper: time.Second},
	{label: "[1000ms, 3000ms)", upper: 3000 * time.Millisecond},
	{label: "[3000ms, +inf)", upper: 0},
}

// Compute 根据收集到的结果生成统计信息。
//
// totalDuration 由调用方测量并传入，本函数不自行计时，便于测试构造固定时长。
func Compute(results []model.ProbeResult, totalDuration time.Duration) Statistics {
	stats := Statistics{
		Total:         len(results),
		TotalDuration: totalDuration,
	}

	durations := make([]time.Duration, 0, len(results))
	successes := make([]model.ProbeResult, 0, len(results))

	for _, result := range results {
		if result.Success {
			stats.Succeeded++
			durations = append(durations, result.ResponseTime)
			successes = append(successes, result)
		}
	}
	stats.Failed = stats.Total - stats.Succeeded

	stats.Latency = computeLatency(durations)
	stats.Distribution = computeDistribution(durations)
	stats.Slowest = slowest(successes, 3)

	return stats
}

// computeLatency 计算成功目标的响应时间统计，使用 nearest-rank 计算分位数。
func computeLatency(durations []time.Duration) LatencyStats {
	if len(durations) == 0 {
		return LatencyStats{}
	}

	// 排序副本，避免修改调用方传入的切片（虽然有拷贝，但这里显式隔离更安全）。
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	return LatencyStats{
		Samples: len(sorted),
		Min:     sorted[0],
		Max:     sorted[len(sorted)-1],
		Avg:     total / time.Duration(len(sorted)),
		P50:     percentile(sorted, 0.50),
		P95:     percentile(sorted, 0.95),
	}
}

// percentile 使用 nearest-rank 方法计算分位数（doc 10.4）：
// rank = ceil(p * N)，取排序后第 rank 个元素（从 1 开始）。
//
// 入参必须已按升序排列且非空。样本较少时 P95 可能等于最大值，属于该方法的正常结果。
func percentile(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}

	rank := int(math.Ceil(p * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// computeDistribution 统计各响应时间区间的目标数量，总和等于样本数量。
func computeDistribution(durations []time.Duration) []Bucket {
	buckets := make([]Bucket, len(bucketUppers))
	for i, def := range bucketUppers {
		buckets[i] = Bucket{Label: def.label}
	}

	for _, d := range durations {
		index := len(bucketUppers) - 1 // 默认落入最后一个区间（>= 3000ms）
		for i, def := range bucketUppers {
			// 左闭右开：d == upper 时归入下一个区间。
			if def.upper > 0 && d < def.upper {
				index = i
				break
			}
		}
		buckets[index].Count++
	}

	return buckets
}

// slowest 返回按响应时间降序排列的最多 limit 条成功结果。
func slowest(successes []model.ProbeResult, limit int) []model.ProbeResult {
	if len(successes) == 0 {
		return nil
	}

	sorted := make([]model.ProbeResult, len(successes))
	copy(sorted, successes)
	// 使用稳定排序，响应时间相同时保持原有顺序，结果可复现。
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].ResponseTime > sorted[j].ResponseTime
	})

	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}
