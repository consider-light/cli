package reporter

import (
	"testing"
	"time"

	"service-health-checker/internal/model"
)

// result 构造一条测试用探测结果。
func result(name string, success bool, latency time.Duration) model.ProbeResult {
	return model.ProbeResult{
		Name:         name,
		Type:         model.TypeHTTP,
		Address:      "http://example.test",
		Success:      success,
		StatusCode:   200,
		ResponseTime: latency,
		Attempts:     1,
	}
}

func TestComputeMixedResults(t *testing.T) {
	results := []model.ProbeResult{
		result("fast", true, 10*time.Millisecond),
		result("mid", true, 200*time.Millisecond),
		result("slow", true, 3000*time.Millisecond),
		result("broken", false, 9999*time.Millisecond), // 失败目标不参与延迟统计
	}

	stats := Compute(results, 5*time.Second)

	if stats.Total != 4 || stats.Succeeded != 3 || stats.Failed != 1 {
		t.Errorf("数量统计错误: %+v", stats)
	}
	if got := stats.SuccessRate(); got != 75 {
		t.Errorf("成功率应为 75，实际为 %v", got)
	}
	if stats.TotalDuration != 5*time.Second {
		t.Errorf("总耗时应为 5s，实际为 %v", stats.TotalDuration)
	}

	want := LatencyStats{
		Samples: 3,
		Min:     10 * time.Millisecond,
		Max:     3000 * time.Millisecond,
		Avg:     1070 * time.Millisecond,
		P50:     200 * time.Millisecond,
		P95:     3000 * time.Millisecond,
	}
	if stats.Latency != want {
		t.Errorf("延迟统计不符合预期\n实际: %+v\n期望: %+v", stats.Latency, want)
	}

	if len(stats.Slowest) != 3 {
		t.Fatalf("最慢服务应有 3 条，实际为 %d", len(stats.Slowest))
	}
	wantOrder := []string{"slow", "mid", "fast"}
	for i, name := range wantOrder {
		if stats.Slowest[i].Name != name {
			t.Errorf("第 %d 慢的服务应为 %q，实际为 %q", i+1, name, stats.Slowest[i].Name)
		}
	}
}

func TestComputeNoSuccess(t *testing.T) {
	stats := Compute([]model.ProbeResult{
		result("a", false, 3*time.Second),
		result("b", false, 100*time.Millisecond),
	}, time.Second)

	if stats.Succeeded != 0 || stats.Failed != 2 {
		t.Errorf("数量统计错误: %+v", stats)
	}
	if got := stats.SuccessRate(); got != 0 {
		t.Errorf("无成功目标时成功率应为 0，实际为 %v", got)
	}
	if stats.Latency.Samples != 0 {
		t.Errorf("无成功目标时不应有延迟样本，实际为 %d", stats.Latency.Samples)
	}
	if stats.Latency != (LatencyStats{}) {
		t.Errorf("无成功目标时延迟统计应全为零值，实际为 %+v", stats.Latency)
	}
	if stats.Slowest != nil {
		t.Errorf("无成功目标时最慢服务应为空，实际为 %#v", stats.Slowest)
	}
	// 分布仍然输出 5 个区间，但计数全为 0，且不会除以 0。
	if len(stats.Distribution) != 5 {
		t.Fatalf("分布应有 5 个区间，实际为 %d", len(stats.Distribution))
	}
	for _, bucket := range stats.Distribution {
		if bucket.Count != 0 {
			t.Errorf("区间 %s 计数应为 0，实际为 %d", bucket.Label, bucket.Count)
		}
	}
}

func TestComputeEmpty(t *testing.T) {
	stats := Compute(nil, 0)

	if stats.Total != 0 || stats.Succeeded != 0 || stats.Failed != 0 {
		t.Errorf("空结果统计错误: %+v", stats)
	}
	if got := stats.SuccessRate(); got != 0 {
		t.Errorf("总数为 0 时成功率应为 0（不能除以 0），实际为 %v", got)
	}
}

// TestPercentileNearestRank 验证 nearest-rank 口径（doc 10.4）。
func TestPercentileNearestRank(t *testing.T) {
	sorted := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
	}

	tests := []struct {
		p    float64
		want time.Duration
	}{
		{p: 0.50, want: 20 * time.Millisecond}, // rank = ceil(2.0) = 2
		{p: 0.95, want: 40 * time.Millisecond}, // rank = ceil(3.8) = 4
		{p: 0.25, want: 10 * time.Millisecond}, // rank = ceil(1.0) = 1
		{p: 0, want: 10 * time.Millisecond},    // rank 收敛到 1
		{p: 1, want: 40 * time.Millisecond},    // rank 收敛到 N
	}

	for _, tc := range tests {
		if got := percentile(sorted, tc.p); got != tc.want {
			t.Errorf("p=%.2f 时百分位应为 %v，实际为 %v", tc.p, tc.want, got)
		}
	}

	// 单元素时 P50/P95 都等于该元素。
	single := []time.Duration{15 * time.Millisecond}
	if percentile(single, 0.5) != 15*time.Millisecond || percentile(single, 0.95) != 15*time.Millisecond {
		t.Error("单元素时 P50/P95 应等于该元素")
	}

	// 空切片不应 panic。
	if got := percentile(nil, 0.95); got != 0 {
		t.Errorf("空切片应返回 0，实际为 %v", got)
	}
}

// TestDistributionBoundaries 验证区间为左闭右开，边界值落入右侧区间。
func TestDistributionBoundaries(t *testing.T) {
	durations := []time.Duration{
		0,
		99 * time.Millisecond,
		100 * time.Millisecond,
		499 * time.Millisecond,
		500 * time.Millisecond,
		999 * time.Millisecond,
		1000 * time.Millisecond,
		2999 * time.Millisecond,
		3000 * time.Millisecond,
	}

	buckets := computeDistribution(durations)

	wantCounts := []int{2, 2, 2, 2, 1}
	if len(buckets) != len(wantCounts) {
		t.Fatalf("区间数量应为 %d，实际为 %d", len(wantCounts), len(buckets))
	}

	total := 0
	for i, bucket := range buckets {
		if bucket.Count != wantCounts[i] {
			t.Errorf("区间 %s 计数应为 %d，实际为 %d", bucket.Label, wantCounts[i], bucket.Count)
		}
		total += bucket.Count
	}
	if total != len(durations) {
		t.Errorf("各区间计数之和应等于样本数 %d，实际为 %d", len(durations), total)
	}
}

// TestSlowestLimit 验证最慢服务最多 3 条且按降序排列。
func TestSlowestLimit(t *testing.T) {
	successes := []model.ProbeResult{
		result("a", true, 5*time.Millisecond),
		result("b", true, 1*time.Millisecond),
		result("c", true, 9*time.Millisecond),
		result("d", true, 3*time.Millisecond),
		result("e", true, 7*time.Millisecond),
	}

	got := slowest(successes, 3)
	if len(got) != 3 {
		t.Fatalf("应返回 3 条，实际为 %d", len(got))
	}

	wantOrder := []string{"c", "e", "a"}
	for i, name := range wantOrder {
		if got[i].Name != name {
			t.Errorf("第 %d 条应为 %q，实际为 %q", i+1, name, got[i].Name)
		}
	}
}
