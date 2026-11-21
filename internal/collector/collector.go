// Package collector 负责从结果 Channel 中完整读取所有探测结果。
//
// Collector 只做读取与累积，不修改结果内容、不执行探测、不做统计，因此可以放心地
// 交给主流程同步执行（doc 4.2 / 9.5）。
package collector

import "service-health-checker/internal/model"

// Collect 持续读取 results，直到 Channel 被关闭且缓冲数据全部取完。
//
// range 会一直阻塞到有数据或 Channel 关闭；因为 Channel 由 Scheduler 的收尾
// Goroutine 在所有生产者退出后关闭，所以本函数返回即代表"全部目标已完成"，
// App 可以安全地进入报告阶段。
//
// 返回值可能为 nil（没有任何目标或没有结果），调用方应按"零条结果"处理。
func Collect(results <-chan model.ProbeResult) []model.ProbeResult {
	var collected []model.ProbeResult
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}
