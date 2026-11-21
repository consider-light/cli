// Package tests 存放跨模块集成测试。
//
// 单元测试按包就近存放于各 internal 包，这里只做"真实二进制 + 真实配置文件"的
// 端到端验证：编译一次 CLI，然后用不同的配置文件启动进程，检查标准输出、
// 标准错误与退出码。因此本包不 import 任何 internal 包，只通过进程边界观察行为。
package tests

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// binaryPath 是 TestMain 编译出的 CLI 可执行文件路径。
var binaryPath string

// buildErr 记录编译失败的原因，供各测试用 t.Fatal 报出。
var buildErr error

// TestMain 先编译一次 CLI，再运行全部集成测试。
//
// 使用 os.MkdirTemp 而不是 t.TempDir，是因为 TestMain 中拿不到 *testing.T；
// 目录在全部测试结束后统一删除。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "health-checker-integration")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建临时目录失败: %v\n", err)
		os.Exit(1)
	}

	name := "health-checker"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binaryPath = filepath.Join(dir, name)

	// 测试进程的工作目录是本包目录（tests/），项目根目录在其上一级。
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/health-checker")
	build.Dir = ".."
	var buildOutput bytes.Buffer
	build.Stdout = &buildOutput
	build.Stderr = &buildOutput
	if err := build.Run(); err != nil {
		buildErr = fmt.Errorf("编译 CLI 失败: %w\n%s", err, buildOutput.String())
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// requireBinary 在编译失败或找不到可执行文件时终止当前测试。
func requireBinary(t *testing.T) {
	t.Helper()

	if buildErr != nil {
		t.Fatal(buildErr)
	}
	if _, err := os.Stat(binaryPath); err != nil {
		t.Fatalf("找不到编译产物 %s: %v", binaryPath, err)
	}
}

// writeConfig 把 JSON 内容写入临时配置文件并返回路径。
func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	return path
}

// runCLI 启动一次 CLI 并返回退出码与输出。
//
// 工作目录是一个独立的临时目录：CLI 会在当前目录生成 monitor-log-*.log，
// 不能让集成测试把日志文件写进 tests/ 包目录。
//
// 进程本身不应挂起：全部目标都在本地，探测耗时上限由 --timeout 控制。
func runCLI(t *testing.T, configPath string, extraArgs ...string) (int, string, string) {
	t.Helper()

	return runCLIInDir(t, t.TempDir(), configPath, extraArgs...)
}

// runCLIInDir 在指定工作目录启动 CLI，便于检查进程在当前目录生成的产物。
func runCLIInDir(t *testing.T, workDir, configPath string, extraArgs ...string) (int, string, string) {
	t.Helper()
	requireBinary(t)

	args := append([]string{"--config", configPath}, extraArgs...)
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("启动 CLI 失败: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return code, stdout.String(), stderr.String()
}

// TestEndToEndSuccess 验证"配置 -> 并发探测 -> 报告 -> 退出码 0"的完整链路：
// 使用本地 HTTP 服务与本地 TCP 监听，不访问公共网络。
func TestEndToEndSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 第一个目标故意更慢，用于验证报告顺序由配置决定而不是完成顺序。
		if r.URL.Path == "/slow" {
			time.Sleep(120 * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "hello health checker")
	}))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动本地 Listener 失败: %v", err)
	}
	defer ln.Close()

	path := writeConfig(t, fmt.Sprintf(`{
  "targets": [
    {"name": "慢速HTTP", "type": "http", "address": "%s/slow", "expected_status": 200, "contains": "health"},
    {"name": "快速HTTP", "type": "http", "address": "%s/fast", "expected_status": 200},
    {"name": "本地TCP", "type": "tcp", "address": %q}
  ]
}`, srv.URL, srv.URL, ln.Addr().String()))

	code, stdout, stderr := runCLI(t, path, "--timeout", "2", "-v")

	if code != 0 {
		t.Fatalf("全部目标成功时退出码应为 0，实际为 %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("成功路径不应写 stderr，实际为: %s", stderr)
	}

	// 报告各部分齐全（doc 10.1）。
	for _, want := range []string{
		"========== Service Health Report ==========",
		"Total Targets : 3",
		"Succeeded     : 3",
		"Failed        : 0",
		"Success Rate  : 100.00%",
		"---------------- Target Details ----------------",
		"---------------- Latency Statistics ----------------",
		"---------------- Latency Distribution ----------------",
		"---------------- Slowest Services TOP 3 ----------------",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout 缺少 %q\n实际输出:\n%s", want, stdout)
		}
	}

	// 报告必须晚于任务日志（doc 9.5）。
	reportAt := strings.Index(stdout, "========== Service Health Report ==========")
	if lastEvent := strings.LastIndex(stdout, "[SUCCESS]"); reportAt < lastEvent {
		t.Errorf("报告应位于任务日志之后\n实际输出:\n%s", stdout)
	}

	// 明细按配置顺序输出：慢速目标先完成与否都不影响位置。
	detailsAt := strings.Index(stdout, "---------------- Target Details ----------------")
	if detailsAt < 0 {
		t.Fatalf("报告缺少目标明细区块\n%s", stdout)
	}
	details := stdout[detailsAt:]
	slowAt := strings.Index(details, "慢速HTTP")
	fastAt := strings.Index(details, "快速HTTP")
	tcpAt := strings.Index(details, "本地TCP")
	if slowAt < 0 || fastAt < 0 || tcpAt < 0 {
		t.Fatalf("明细缺少目标行\n%s", details)
	}
	if !(slowAt < fastAt && fastAt < tcpAt) {
		t.Errorf("明细顺序应与配置一致（慢速HTTP -> 快速HTTP -> 本地TCP）\n%s", details)
	}

	// 分布计数之和等于成功数（doc 10.3）。
	if sum := sumDistribution(details); sum != 3 {
		t.Errorf("分布计数之和应为 3，实际为 %d\n%s", sum, details)
	}
}

// TestEndToEndWritesLogFile 验证真实二进制会在运行目录生成 monitor-log-*.log，
// 文件内含运行头部与完整报告，终端同时提示落点。
func TestEndToEndWritesLogFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(`{
  "targets": [
    {"name": "日志HTTP", "type": "http", "address": %q}
  ]
}`, srv.URL))

	workDir := t.TempDir()
	code, stdout, stderr := runCLIInDir(t, workDir, path, "--timeout", "2")

	if code != 0 {
		t.Fatalf("全部目标成功时退出码应为 0，实际为 %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("成功路径不应写 stderr，实际为: %s", stderr)
	}

	matches, err := filepath.Glob(filepath.Join(workDir, "monitor-log-*.log"))
	if err != nil {
		t.Fatalf("扫描日志文件失败: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("运行目录应生成 1 个日志文件，实际为 %d 个: %v", len(matches), matches)
	}

	// 终端提示的是相对当前目录的文件名，与真实文件同名。
	if base := filepath.Base(matches[0]); !strings.Contains(stdout, base) {
		t.Errorf("stdout 应提示日志文件名 %q\n实际输出:\n%s", base, stdout)
	}

	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	text := string(content)

	for _, want := range []string{
		"# 服务健康探测运行日志",
		"# 配置文件: " + path,
		"Total Targets : 1",
		"Succeeded     : 1",
		"========== Service Health Report ==========",
		"---------------- Slowest Services TOP 3 ----------------",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("日志文件缺少 %q\n文件内容:\n%s", want, text)
		}
	}

	// 报告必须位于运行日志之后，与终端的顺序一致。
	reportAt := strings.Index(text, "========== Service Health Report ==========")
	if infoAt := strings.Index(text, "[INFO] Loaded"); infoAt >= 0 && reportAt < infoAt {
		t.Errorf("文件中的报告应位于运行日志之后\n文件内容:\n%s", text)
	}
}

// TestEndToEndPartialFailure 验证失败目标不影响其他目标，且退出码为 1。
func TestEndToEndPartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 关闭监听后端口必然拒绝连接，且不依赖任何外部环境。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动本地 Listener 失败: %v", err)
	}
	deadAddr := ln.Addr().String()
	ln.Close()

	path := writeConfig(t, fmt.Sprintf(`{
  "targets": [
    {"name": "正常服务", "type": "http", "address": %q, "expected_status": 200},
    {"name": "关闭的端口", "type": "tcp", "address": %q}
  ]
}`, srv.URL, deadAddr))

	code, stdout, _ := runCLI(t, path, "--timeout", "2")

	if code != 1 {
		t.Fatalf("存在失败目标时退出码应为 1，实际为 %d\nstdout:\n%s", code, stdout)
	}
	for _, want := range []string{"Succeeded     : 1", "Failed        : 1", "Success Rate  : 50.00%", "关闭的端口", "FAILED"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout 缺少 %q\n实际输出:\n%s", want, stdout)
		}
	}
	// 失败目标不参与延迟统计与 TOP 3（doc 10.2）。
	if strings.Contains(stdout, "1. 关闭的端口") {
		t.Errorf("失败目标不应出现在最慢服务排名中\n%s", stdout)
	}
}

// TestEndToEndStartupError 验证配置文件不存在时以退出码 2 结束，且不输出报告。
func TestEndToEndStartupError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-exist.json")

	code, stdout, stderr := runCLI(t, missing)

	if code != 2 {
		t.Fatalf("配置读取失败时退出码应为 2，实际为 %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "Service Health Report") {
		t.Error("启动失败时不应输出报告")
	}
	if !strings.Contains(stderr, "[ERROR]") {
		t.Errorf("stderr 应包含错误信息，实际为: %s", stderr)
	}
}

// sumDistribution 汇总报告中延迟分布的计数。
func sumDistribution(report string) int {
	at := strings.Index(report, "---------------- Latency Distribution ----------------")
	if at < 0 {
		return 0
	}

	sum := 0
	for _, line := range strings.Split(report[at:], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") || !strings.Contains(line, ":") {
			continue
		}
		var count int
		if _, err := fmt.Sscanf(line[strings.LastIndex(line, ":")+1:], "%d", &count); err == nil {
			sum += count
		}
	}
	return sum
}
