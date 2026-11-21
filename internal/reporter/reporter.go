// Package reporter 负责终端报告输出与统计计算。
//
// 报告只写调用方提供的 io.Writer（正式运行是标准输出），不生成任何 JSON、
// Markdown 或其他文件（doc 1.3）。统计口径严格遵循 doc 10.2：只有最终成功的
// 目标参与延迟统计与最慢服务排名。
package reporter

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"service-health-checker/internal/model"
)

// maxErrorWidth 限制 ERROR 列的展示宽度，避免单个超长错误把表格挤变形。
//
// 取值依据终端宽度预算：其余六列加分隔空格共占 61 列（名称 12 + 类型 4 +
// 状态 7 + 耗时 7 + 尝试次数 8 + 状态码 11，每列后再加 2 个空格），
// 61 + 55 = 116 列，可完整显示在常见的 120 列终端内而不折行。
const maxErrorWidth = 55

// Write 输出完整的健康检查报告。
//
// totalDuration 是从启动调度到收集完全部结果的墙钟时间，由调用方测量；
// 结果数量为 0 时同样成立（不 panic，成功率显示 0.00%，延迟显示 N/A）。
func Write(w io.Writer, results []model.ProbeResult, totalDuration time.Duration) {
	stats := Compute(results, totalDuration)

	fmt.Fprintln(w, "========== Service Health Report ==========")
	fmt.Fprintf(w, "Total Targets : %d\n", stats.Total)
	fmt.Fprintf(w, "Succeeded     : %d\n", stats.Succeeded)
	fmt.Fprintf(w, "Failed        : %d\n", stats.Failed)
	fmt.Fprintf(w, "Success Rate  : %.2f%%\n", stats.SuccessRate())
	fmt.Fprintf(w, "Total Duration: %s\n", formatDuration(stats.TotalDuration))

	fmt.Fprintln(w)
	fmt.Fprintln(w, "---------------- Target Details ----------------")
	writeTargetTable(w, results)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "---------------- Latency Statistics ----------------")
	writeLatencyStats(w, stats.Latency)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "---------------- Latency Distribution ----------------")
	writeDistribution(w, stats.Distribution)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "---------------- Slowest Services TOP 3 ----------------")
	writeSlowest(w, stats.Slowest)

	fmt.Fprintln(w, "============================================")
}

// writeTargetTable 输出每个目标的明细。
func writeTargetTable(w io.Writer, results []model.ProbeResult) {
	header := []string{"NAME", "TYPE", "STATUS", "LATENCY", "ATTEMPTS", "HTTP STATUS", "ERROR"}

	rows := make([][]string, 0, len(results))
	for _, result := range results {
		status := "SUCCESS"
		if !result.Success {
			status = "FAILED"
		}

		// TCP 探测不使用状态码；HTTP 探测在未拿到响应（如 DNS 失败）时同样展示 -。
		httpStatus := "-"
		if result.Type == model.TypeHTTP && result.StatusCode > 0 {
			httpStatus = strconv.Itoa(result.StatusCode)
		}

		errorText := result.Error
		if errorText == "" {
			errorText = "-"
		}

		rows = append(rows, []string{
			result.Name,
			strings.ToUpper(result.Type),
			status,
			formatDuration(result.ResponseTime),
			strconv.Itoa(result.Attempts),
			httpStatus,
			truncate(errorText, maxErrorWidth),
		})
	}

	writeTable(w, header, rows)
}

// writeLatencyStats 输出成功目标的延迟统计，无成功目标时全部显示 N/A。
func writeLatencyStats(w io.Writer, stats LatencyStats) {
	if stats.Samples == 0 {
		fmt.Fprintln(w, "Min : N/A")
		fmt.Fprintln(w, "Avg : N/A")
		fmt.Fprintln(w, "P50 : N/A")
		fmt.Fprintln(w, "P95 : N/A")
		fmt.Fprintln(w, "Max : N/A")
		return
	}

	fmt.Fprintf(w, "Min : %s\n", formatDuration(stats.Min))
	fmt.Fprintf(w, "Avg : %s\n", formatDuration(stats.Avg))
	fmt.Fprintf(w, "P50 : %s\n", formatDuration(stats.P50))
	fmt.Fprintf(w, "P95 : %s\n", formatDuration(stats.P95))
	fmt.Fprintf(w, "Max : %s\n", formatDuration(stats.Max))
}

// writeDistribution 输出响应时间分布，区间顺序与统计口径一致。
func writeDistribution(w io.Writer, buckets []Bucket) {
	width := 0
	for _, bucket := range buckets {
		if lw := displayWidth(bucket.Label); lw > width {
			width = lw
		}
	}

	for _, bucket := range buckets {
		fmt.Fprintf(w, "%s : %d\n", padRight(bucket.Label, width), bucket.Count)
	}
}

// writeSlowest 输出最慢的成功服务排名，无成功目标时显示 N/A。
func writeSlowest(w io.Writer, slowest []model.ProbeResult) {
	if len(slowest) == 0 {
		fmt.Fprintln(w, "N/A")
		return
	}

	width := 0
	for _, result := range slowest {
		if lw := displayWidth(result.Name); lw > width {
			width = lw
		}
	}

	for i, result := range slowest {
		fmt.Fprintf(w, "%d. %s %s\n", i+1, padRight(result.Name, width), formatDuration(result.ResponseTime))
	}
}

// writeTable 按显示宽度对齐输出表格，最后一列不补空格。
func writeTable(w io.Writer, header []string, rows [][]string) {
	widths := make([]int, len(header))
	for i, cell := range header {
		widths[i] = displayWidth(cell)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			if cw := displayWidth(cell); cw > widths[i] {
				widths[i] = cw
			}
		}
	}

	writeRow := func(cells []string) {
		var line strings.Builder
		for i, cell := range cells {
			if i > 0 {
				line.WriteString("  ")
			}
			line.WriteString(cell)
			if i < len(cells)-1 {
				line.WriteString(strings.Repeat(" ", widths[i]-displayWidth(cell)))
			}
		}
		fmt.Fprintln(w, line.String())
	}

	writeRow(header)
	for _, row := range rows {
		writeRow(row)
	}
}

// formatDuration 统一延迟展示格式：
//   - 小于 1 秒使用整数毫秒（0ms 是合法值，Windows 时钟粒度较粗时可能出现）；
//   - 大于等于 1 秒使用两位小数的秒（例如 3.00s），与 doc 10.5 的示意一致。
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0ms"
	}
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

// padRight 用空格把 s 补齐到指定显示宽度。
func padRight(s string, width int) string {
	if padding := width - displayWidth(s); padding > 0 {
		return s + strings.Repeat(" ", padding)
	}
	return s
}

// truncate 按显示宽度截断过长的文本，超出时以省略号结尾。
func truncate(s string, max int) string {
	if max <= 0 || displayWidth(s) <= max {
		return s
	}

	var b strings.Builder
	width := 0
	for _, r := range s {
		rw := runeWidth(r)
		if width+rw > max-1 {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	b.WriteString("…")
	return b.String()
}

// displayWidth 返回字符串在等宽终端中的显示宽度：东亚宽字符按 2 列计算。
//
// text/tabwriter 按 rune 数量计算宽度，含中文时列会错位，因此这里自行计算。
func displayWidth(s string) int {
	width := 0
	for _, r := range s {
		width += runeWidth(r)
	}
	return width
}

// runeWidth 返回单个字符的显示宽度（0/1/2）。
func runeWidth(r rune) int {
	switch {
	case r < 0x20 || r == 0x7F:
		// 控制字符不占显示宽度。
		return 0
	case r >= 0x1100 && (r <= 0x115F ||
		r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x20000 && r <= 0x3FFFD)):
		return 2
	default:
		return 1
	}
}
