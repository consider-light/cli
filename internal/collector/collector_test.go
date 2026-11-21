package collector

import (
	"reflect"
	"testing"
	"time"

	"service-health-checker/internal/model"
)

// sampleResult 构造一条用于比对的探测结果。
func sampleResult(name string, success bool) model.ProbeResult {
	return model.ProbeResult{
		Name:         name,
		Type:         model.TypeHTTP,
		Address:      "http://example.test",
		Success:      success,
		StatusCode:   200,
		ResponseTime: 12 * time.Millisecond,
		Attempts:     1,
	}
}

// TestCollectAllResults 验证在 Channel 关闭前发送的结果会被完整收集，且顺序不变。
func TestCollectAllResults(t *testing.T) {
	results := make(chan model.ProbeResult, 3)
	want := []model.ProbeResult{
		sampleResult("a", true),
		sampleResult("b", false),
		sampleResult("c", true),
	}
	for _, result := range want {
		results <- result
	}
	close(results)

	got := Collect(results)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("收集结果不完整或顺序改变\n实际: %#v\n期望: %#v", got, want)
	}
}

// TestCollectEmptyChannel 验证空 Channel 关闭时返回空结果，而不是阻塞。
func TestCollectEmptyChannel(t *testing.T) {
	results := make(chan model.ProbeResult)
	close(results)

	if got := Collect(results); len(got) != 0 {
		t.Errorf("空 Channel 应返回空结果，实际为 %#v", got)
	}
}

// TestCollectDrainsBufferedResultsAfterClose 验证关闭后缓冲区内的数据仍会被读完。
func TestCollectDrainsBufferedResultsAfterClose(t *testing.T) {
	results := make(chan model.ProbeResult, 4)
	results <- sampleResult("a", true)
	results <- sampleResult("b", true)
	close(results)

	if got := Collect(results); len(got) != 2 {
		t.Errorf("应读取缓冲区中的 2 条结果，实际为 %d", len(got))
	}
}

// TestCollectDoesNotModifyResults 验证 Collector 不修改结果内容。
func TestCollectDoesNotModifyResults(t *testing.T) {
	want := []model.ProbeResult{sampleResult("a", false)}
	results := make(chan model.ProbeResult, 1)
	results <- want[0]
	close(results)

	got := Collect(results)
	if !reflect.DeepEqual(got[0], want[0]) {
		t.Errorf("Collector 不应修改结果\n实际: %#v\n期望: %#v", got[0], want[0])
	}
}

// TestCollectWithConcurrentProducer 让生产者在发送过程中关闭 Channel，
// 验证 Collector 能读到全部数据（在 -race 下同时验证无数据竞争）。
func TestCollectWithConcurrentProducer(t *testing.T) {
	const count = 20
	results := make(chan model.ProbeResult)

	go func() {
		defer close(results)
		for i := 0; i < count; i++ {
			results <- sampleResult("target", true)
		}
	}()

	if got := Collect(results); len(got) != count {
		t.Errorf("应收集到 %d 条结果，实际为 %d", count, len(got))
	}
}
