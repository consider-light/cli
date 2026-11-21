package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseDefaults(t *testing.T) {
	opts, err := Parse(nil)
	if err != nil {
		t.Fatalf("解析默认参数不应失败: %v", err)
	}

	if opts.ConfigPath != "config.json" {
		t.Errorf("默认配置文件应为 config.json，实际为 %q", opts.ConfigPath)
	}
	if opts.Timeout != 3*time.Second {
		t.Errorf("默认超时应为 3s，实际为 %v", opts.Timeout)
	}
	if opts.Verbose {
		t.Error("默认不应开启详细模式")
	}
}

func TestParseAllFlags(t *testing.T) {
	opts, err := Parse([]string{"--config", "./conf/test.json", "--timeout", "5", "-v"})
	if err != nil {
		t.Fatalf("解析合法参数不应失败: %v", err)
	}

	if opts.ConfigPath != "./conf/test.json" {
		t.Errorf("配置路径解析错误，实际为 %q", opts.ConfigPath)
	}
	if opts.Timeout != 5*time.Second {
		t.Errorf("超时应为 5s，实际为 %v", opts.Timeout)
	}
	if !opts.Verbose {
		t.Error("-v 应开启详细模式")
	}
}

// TestParseSingleDashForms 验证 Go flag 风格的单横线写法同样可用。
func TestParseSingleDashForms(t *testing.T) {
	opts, err := Parse([]string{"-config", "a.json", "-timeout", "2"})
	if err != nil {
		t.Fatalf("单横线写法应被接受: %v", err)
	}
	if opts.ConfigPath != "a.json" || opts.Timeout != 2*time.Second {
		t.Errorf("解析结果不符合预期: %#v", opts)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantContain string
	}{
		{name: "超时为 0", args: []string{"--timeout", "0"}, wantContain: "必须大于 0"},
		{name: "超时为负数", args: []string{"--timeout", "-2"}, wantContain: "必须大于 0"},
		{name: "超时非整数", args: []string{"--timeout", "abc"}, wantContain: "参数解析失败"},
		{name: "未知参数", args: []string{"--unknown"}, wantContain: "参数解析失败"},
		{name: "多余位置参数", args: []string{"config.json"}, wantContain: "无法识别的参数"},
		{name: "配置路径为空白", args: []string{"--config", "   "}, wantContain: "--config 不能为空"},
		{name: "配置路径为空串", args: []string{"--config", ""}, wantContain: "--config 不能为空"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := Parse(tc.args)
			if err == nil {
				t.Fatalf("期望返回错误，实际解析成功: %#v", opts)
			}
			if !strings.Contains(err.Error(), tc.wantContain) {
				t.Errorf("错误信息未包含 %q\n实际错误: %s", tc.wantContain, err.Error())
			}
		})
	}
}

func TestParseHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		opts, err := Parse([]string{arg})
		if !errors.Is(err, ErrHelp) {
			t.Errorf("%s 应返回 ErrHelp，实际错误为 %v（opts=%#v）", arg, err, opts)
		}
	}
}

func TestUsage(t *testing.T) {
	var buf bytes.Buffer
	Usage(&buf)

	output := buf.String()
	for _, want := range []string{"--config", "--timeout", "-v", "config.json", "retry_count"} {
		if !strings.Contains(output, want) {
			t.Errorf("用法说明缺少 %q\n实际输出:\n%s", want, output)
		}
	}
}
