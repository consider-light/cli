package reporter

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"service-health-checker/internal/model"
)

func TestWriteReport(t *testing.T) {
	results := []model.ProbeResult{
		{
			Name:         "百度搜索",
			Type:         model.TypeHTTP,
			Address:      "https://www.baidu.com",
			Success:      true,
			StatusCode:   200,
			ResponseTime: 120 * time.Millisecond,
			Attempts:     1,
		},
		{
			Name:         "MySQL服务",
			Type:         model.TypeTCP,
			Address:      "localhost:3306",
			Success:      true,
			ResponseTime: 5 * time.Millisecond,
			Attempts:     2,
		},
		{
			Name:         "无效服务",
			Type:         model.TypeHTTP,
			Address:      "https://not-exist-domain-123.com",
			Success:      false,
			ResponseTime: 3 * time.Second,
			Attempts:     1,
			Error:        "DNS lookup failed",
		},
	}

	var buf bytes.Buffer
	Write(&buf, results, 5*time.Second)
	output := buf.String()

	required := []string{
		"========== Service Health Report ==========",
		"Total Targets : 3",
		"Succeeded     : 2",
		"Failed        : 1",
		"Success Rate  : 66.67%",
		"Total Duration: 5.00s",
		"---------------- Target Details ----------------",
		"NAME",
		"TYPE",
		"STATUS",
		"LATENCY",
		"ATTEMPTS",
		"HTTP STATUS",
		"ERROR",
		"SUCCESS",
		"FAILED",
		"120ms",
		"5ms",
		"3.00s",
		"DNS lookup failed",
		"---------------- Latency Statistics ----------------",
		"Min : 5ms",
		"Avg : 62ms",
		"P50 : 5ms",
		"P95 : 120ms",
		"Max : 120ms",
		"---------------- Latency Distribution ----------------",
		"[0ms, 100ms)",
		"[100ms, 500ms)",
		"[3000ms, +inf)",
		"---------------- Slowest Services TOP 3 ----------------",
		"1. 百度搜索",
		"2. MySQL服务 5ms",
		"============================================",
	}
	for _, want := range required {
		if !strings.Contains(output, want) {
			t.Errorf("报告缺少内容 %q\n实际输出:\n%s", want, output)
		}
	}

	// TCP 目标不应展示 HTTP 状态码；HTTP 目标应展示实际状态码。
	if !strings.Contains(output, "200") {
		t.Error("报告应包含 HTTP 目标的状态码 200")
	}
}

// TestWriteReportWithoutSuccess 验证没有成功目标时延迟统计为 N/A 且不 panic。
func TestWriteReportWithoutSuccess(t *testing.T) {
	results := []model.ProbeResult{
		{Name: "a", Type: model.TypeHTTP, Success: false, Error: "dial tcp: connection refused", Attempts: 1},
		{Name: "b", Type: model.TypeTCP, Success: false, Error: "i/o timeout", Attempts: 2},
	}

	var buf bytes.Buffer
	Write(&buf, results, 2*time.Second)
	output := buf.String()

	if !strings.Contains(output, "Success Rate  : 0.00%") {
		t.Errorf("成功率应显示 0.00%%\n实际输出:\n%s", output)
	}
	for _, label := range []string{"Min : N/A", "Avg : N/A", "P50 : N/A", "P95 : N/A", "Max : N/A"} {
		if !strings.Contains(output, label) {
			t.Errorf("无成功目标时应显示 %q\n实际输出:\n%s", label, output)
		}
	}
	// 最慢服务章节应显示 N/A 而不是空列表。
	if !strings.Contains(output, "---------------- Slowest Services TOP 3 ----------------\nN/A") {
		t.Errorf("无成功目标时最慢服务应显示 N/A\n实际输出:\n%s", output)
	}
}

func TestWriteEmptyReport(t *testing.T) {
	var buf bytes.Buffer
	Write(&buf, nil, 0)

	output := buf.String()
	if !strings.Contains(output, "Total Targets : 0") {
		t.Errorf("空结果应显示 Total Targets : 0\n实际输出:\n%s", output)
	}
	if !strings.Contains(output, "Success Rate  : 0.00%") {
		t.Errorf("空结果成功率应为 0.00%%\n实际输出:\n%s", output)
	}
}

// TestWriteTableAlignsCJK 验证中文名称不会破坏列对齐。
func TestWriteTableAlignsCJK(t *testing.T) {
	results := []model.ProbeResult{
		{Name: "百度搜索", Type: model.TypeHTTP, Success: true, StatusCode: 200, Attempts: 1},
		{Name: "GitHub", Type: model.TypeHTTP, Success: true, StatusCode: 200, Attempts: 1},
	}

	var buf bytes.Buffer
	writeTargetTable(&buf, results)
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("表格应有 3 行，实际为 %d 行:\n%s", len(lines), buf.String())
	}

	// 两行数据中 TYPE 列的起始位置必须相同：中文名"百度搜索"宽 8，"GitHub" 宽 6，
	// 因此 GitHub 需要补 2 个空格，加上列间距共 4 个空格。
	if !strings.Contains(lines[1], "百度搜索  HTTP") {
		t.Errorf("中文行对齐异常: %q", lines[1])
	}
	if !strings.Contains(lines[2], "GitHub    HTTP") {
		t.Errorf("ASCII 行未按显示宽度补齐: %q", lines[2])
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{in: 0, want: "0ms"},
		{in: -time.Millisecond, want: "0ms"},
		{in: 5 * time.Millisecond, want: "5ms"},
		{in: 999 * time.Millisecond, want: "999ms"},
		{in: time.Second, want: "1.00s"},
		{in: 3020 * time.Millisecond, want: "3.02s"},
	}

	for _, tc := range tests {
		if got := formatDuration(tc.in); got != tc.want {
			t.Errorf("formatDuration(%v) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncateKeepsWidth(t *testing.T) {
	long := strings.Repeat("错误", 60) // 显示宽度 120
	got := truncate(long, 20)

	if w := displayWidth(got); w > 20 {
		t.Errorf("截断后显示宽度应不超过 20，实际为 %d (%q)", w, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("截断后应以省略号结尾，实际为 %q", got)
	}

	short := "short"
	if truncate(short, 20) != short {
		t.Error("未超长的文本不应被截断")
	}
}

// TestReportFitsTerminalWidth 验证最坏情况下（最长服务名 + 最长错误信息）报告仍能完整
// 显示在 120 列终端内，不会因自动折行破坏表格对齐。
func TestReportFitsTerminalWidth(t *testing.T) {
	results := []model.ProbeResult{
		{
			Name:         "慢速响应模拟", // 显示宽度 12，是配置中最长的名称
			Type:         model.TypeHTTP,
			Address:      "https://httpbin.org/delay/5",
			Success:      false,
			ResponseTime: 3 * time.Second,
			Attempts:     4,
			Error:        strings.Repeat("连接失败", 40),
		},
	}

	var buf bytes.Buffer
	Write(&buf, results, 4*time.Second)

	for _, line := range strings.Split(buf.String(), "\n") {
		if w := displayWidth(line); w > 120 {
			t.Errorf("报告行宽 %d 超过 120 列，终端会折行: %q", w, line)
		}
	}
}

func TestDisplayWidth(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{in: "", want: 0},
		{in: "abc", want: 3},
		{in: "百度搜索", want: 8},
		{in: "GitHub", want: 6},
		{in: "[0ms, 100ms)", want: 12},
	}

	for _, tc := range tests {
		if got := displayWidth(tc.in); got != tc.want {
			t.Errorf("displayWidth(%q) = %d，期望 %d", tc.in, got, tc.want)
		}
	}
}
