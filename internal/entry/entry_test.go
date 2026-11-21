package entry

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// workInTempDir 把工作目录切到临时目录。
//
// Run 会按需求在当前目录生成 monitor-log-*.log，测试不能让这些文件落进包目录。
func workInTempDir(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
}

func TestRunHelpExitsZero(t *testing.T) {
	workInTempDir(t)

	var stdout, stderr bytes.Buffer

	code := Run([]string{"-h"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("显示帮助信息时退出码应为 0，实际为 %d", code)
	}
	if !strings.Contains(stdout.String(), "用法:") {
		t.Errorf("stdout 应包含用法说明，实际为:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("帮助信息不应写入 stderr，实际为: %s", stderr.String())
	}
}

func TestRunInvalidArgs(t *testing.T) {
	workInTempDir(t)

	var stdout, stderr bytes.Buffer

	code := Run([]string{"--timeout", "0"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("参数非法时退出码应为 2，实际为 %d", code)
	}
	if !strings.Contains(stderr.String(), "[ERROR]") {
		t.Errorf("stderr 应包含错误提示，实际为:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--timeout") {
		t.Errorf("stderr 应同时输出用法说明，实际为:\n%s", stderr.String())
	}
}

func TestRunMissingConfig(t *testing.T) {
	workInTempDir(t)

	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "missing.json")

	code := Run([]string{"--config", path}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("配置文件缺失时退出码应为 2，实际为 %d", code)
	}
	if !strings.Contains(stderr.String(), "读取配置文件") {
		t.Errorf("stderr 应说明读取失败，实际为:\n%s", stderr.String())
	}
}
