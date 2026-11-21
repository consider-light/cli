package app

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"service-health-checker/internal/cli"
	"service-health-checker/internal/model"
	"service-health-checker/internal/probe"
)

// writeConfig 把 JSON 内容写入临时配置文件并返回路径。
func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	return path
}

// runApp 执行一次 App 并返回退出码与两个输出流的内容。registry 为 nil 时使用默认注册表。
//
// 日志文件统一写到临时目录：测试不应在包目录里留下 monitor-log-*.log。
func runApp(t *testing.T, registry *probe.Registry, opts cli.Options) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	application := New(opts, &stdout, &stderr)
	if registry != nil {
		application = NewWithRegistry(opts, registry, &stdout, &stderr)
	}
	application = application.WithLogDir(t.TempDir())

	code := application.Run(context.Background())
	return code, stdout.String(), stderr.String()
}

// runAppWithLogDir 在指定目录生成日志文件后执行一次 App。
func runAppWithLogDir(t *testing.T, logDir string, opts cli.Options) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	application := New(opts, &stdout, &stderr).WithLogDir(logDir)

	code := application.Run(context.Background())
	return code, stdout.String(), stderr.String()
}

// TestRunAllSucceed 使用本地 httptest 服务与本地 TCP 监听完成一次端到端流程，
// 不访问任何公共网络。
func TestRunAllSucceed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动本地 Listener 失败: %v", err)
	}
	defer ln.Close()

	path := writeConfig(t, fmt.Sprintf(`{
  "targets": [
    {"name": "本地HTTP", "type": "http", "address": %q, "expected_status": 200},
    {"name": "本地TCP", "type": "tcp", "address": %q}
  ]
}`, srv.URL, ln.Addr().String()))

	code, stdout, stderr := runApp(t, nil, cli.Options{
		ConfigPath: path,
		Timeout:    2 * time.Second,
		Verbose:    true,
	})

	if code != ExitOK {
		t.Fatalf("全部目标成功时退出码应为 0，实际为 %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("成功路径不应写 stderr，实际为: %s", stderr)
	}

	for _, want := range []string{
		"[INFO] Loaded 2 targets",
		"[INFO] Starting service health checks...",
		"[RUNNING] 本地HTTP | HTTP |",
		"[RUNNING] 本地TCP | TCP |",
		"[SUCCESS] 本地HTTP",
		"[SUCCESS] 本地TCP",
		"========== Service Health Report ==========",
		"Total Targets : 2",
		"Succeeded     : 2",
		"Failed        : 0",
		"Success Rate  : 100.00%",
		"[INFO] Report finished.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout 缺少 %q\n实际输出:\n%s", want, stdout)
		}
	}

	// 报告必须在所有任务结束之后才输出，不能与任务日志交错。
	reportIndex := strings.Index(stdout, "========== Service Health Report ==========")
	lastEventIndex := strings.LastIndex(stdout, "[SUCCESS]")
	if reportIndex < lastEventIndex {
		t.Errorf("报告应位于任务日志之后\n实际输出:\n%s", stdout)
	}
}

func TestRunWithFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(`{
  "targets": [
    {"name": "期望200实际404", "type": "http", "address": %q, "expected_status": 200}
  ]
}`, srv.URL))

	// 非详细模式：不输出 RUNNING / FAILED 等任务日志，但必须输出报告。
	code, stdout, stderr := runApp(t, nil, cli.Options{
		ConfigPath: path,
		Timeout:    2 * time.Second,
		Verbose:    false,
	})

	if code != ExitTargetFailed {
		t.Fatalf("存在失败目标时退出码应为 1，实际为 %d\nstdout:\n%s", code, stdout)
	}
	if stderr != "" {
		t.Errorf("探测失败不应写 stderr，实际为: %s", stderr)
	}
	for _, want := range []string{"Failed        : 1", "FAILED", "期望200实际404"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout 缺少 %q\n实际输出:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"[RUNNING]", "[FAILED]"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("非详细模式不应输出 %q\n实际输出:\n%s", unwanted, stdout)
		}
	}
}

func TestRunConfigValidationError(t *testing.T) {
	path := writeConfig(t, `{"targets": []}`)

	code, stdout, stderr := runApp(t, nil, cli.Options{ConfigPath: path, Timeout: time.Second})

	if code != ExitStartupError {
		t.Fatalf("配置校验失败时退出码应为 2，实际为 %d", code)
	}
	if !strings.Contains(stderr, "targets 不能为空") {
		t.Errorf("stderr 应说明校验失败原因，实际为: %s", stderr)
	}
	if strings.Contains(stdout, "Service Health Report") {
		t.Error("配置错误时不应输出报告")
	}
}

func TestRunMissingConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-exist.json")

	code, _, stderr := runApp(t, nil, cli.Options{ConfigPath: path, Timeout: time.Second})

	if code != ExitStartupError {
		t.Fatalf("配置文件不存在时退出码应为 2，实际为 %d", code)
	}
	if !strings.Contains(stderr, "读取配置文件") {
		t.Errorf("stderr 应说明读取失败，实际为: %s", stderr)
	}
}

// TestRunUnsupportedProtocol 验证启动前的协议预校验会拦截注册表缺失的协议类型，
// 且此时不会进入调度（因此不会产生 1 秒人工延迟）。
func TestRunUnsupportedProtocol(t *testing.T) {
	path := writeConfig(t, `{"targets": [{"name": "A", "type": "http", "address": "http://example.test"}]}`)

	code, _, stderr := runApp(t, probe.NewRegistry(), cli.Options{ConfigPath: path, Timeout: time.Second})

	if code != ExitStartupError {
		t.Fatalf("注册表缺少协议时退出码应为 2，实际为 %d", code)
	}
	if !strings.Contains(stderr, "协议类型非法") {
		t.Errorf("stderr 应说明协议不受支持，实际为: %s", stderr)
	}
}

// TestOrderByConfig 验证结果按配置顺序重排，且不修改传入切片。
func TestOrderByConfig(t *testing.T) {
	targets := []model.Target{
		{Name: "A", Type: model.TypeHTTP, Address: "http://a.test"},
		{Name: "B", Type: model.TypeHTTP, Address: "http://b.test"},
		{Name: "C", Type: model.TypeTCP, Address: "127.0.0.1:1"},
	}
	// 完成顺序与配置顺序完全相反。
	results := []model.ProbeResult{
		{Name: "C", Type: model.TypeTCP, Address: "127.0.0.1:1", Success: true},
		{Name: "B", Type: model.TypeHTTP, Address: "http://b.test", Success: true},
		{Name: "A", Type: model.TypeHTTP, Address: "http://a.test", Success: false},
	}

	ordered := orderByConfig(results, targets)

	want := []string{"A", "B", "C"}
	for i, name := range want {
		if ordered[i].Name != name {
			t.Fatalf("第 %d 个结果应为 %q，实际为 %q", i, name, ordered[i].Name)
		}
	}

	// 必须返回新切片：就地排序会打乱调用方（以及潜在的并发读者）看到的结果。
	if results[0].Name != "C" {
		t.Errorf("orderByConfig 不应修改传入的切片，实际首元素为 %q", results[0].Name)
	}
}

// TestOrderByConfigKeepsUnknownResultsLast 验证不在配置中的结果排在末尾且保持相对顺序。
func TestOrderByConfigKeepsUnknownResultsLast(t *testing.T) {
	targets := []model.Target{{Name: "A", Type: model.TypeHTTP, Address: "http://a.test"}}
	results := []model.ProbeResult{
		{Name: "X", Type: model.TypeHTTP, Address: "http://x.test"},
		{Name: "A", Type: model.TypeHTTP, Address: "http://a.test"},
		{Name: "Y", Type: model.TypeHTTP, Address: "http://y.test"},
	}

	ordered := orderByConfig(results, targets)

	if got := []string{ordered[0].Name, ordered[1].Name, ordered[2].Name}; got[0] != "A" || got[1] != "X" || got[2] != "Y" {
		t.Errorf("期望顺序 [A X Y]，实际为 %v", got)
	}

	// 空输入应原样返回，不 panic。
	if got := orderByConfig(nil, targets); len(got) != 0 {
		t.Errorf("空结果应原样返回，实际长度为 %d", len(got))
	}
	if got := orderByConfig(results, nil); len(got) != len(results) {
		t.Errorf("空配置应原样返回全部结果，实际长度为 %d", len(got))
	}
}

// TestReportFollowsConfigOrder 验证并发完成顺序不影响报告中的目标明细顺序：
// 配置中排第一的目标是慢速目标，最后完成，但报告里必须仍排在第一行。
func TestReportFollowsConfigOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(150 * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(`{
  "targets": [
    {"name": "慢目标", "type": "http", "address": "%s/slow"},
    {"name": "快目标", "type": "http", "address": "%s/fast"}
  ]
}`, srv.URL, srv.URL))

	code, stdout, _ := runApp(t, nil, cli.Options{ConfigPath: path, Timeout: 2 * time.Second})
	if code != ExitOK {
		t.Fatalf("全部目标成功时退出码应为 0，实际为 %d\n%s", code, stdout)
	}

	detailsAt := strings.Index(stdout, "---------------- Target Details ----------------")
	if detailsAt < 0 {
		t.Fatalf("报告缺少目标明细区块\n%s", stdout)
	}
	details := stdout[detailsAt:]
	slowIndex := strings.Index(details, "慢目标")
	fastIndex := strings.Index(details, "快目标")
	if slowIndex < 0 || fastIndex < 0 {
		t.Fatalf("报告缺少目标明细\n%s", stdout)
	}
	if slowIndex > fastIndex {
		t.Errorf("报告应按配置顺序输出（慢目标在前），实际顺序相反\n%s", stdout)
	}
}

// TestRunWritesLogFile 验证一次运行结束后在指定目录生成 monitor-log-*.log，
// 且文件内容与终端一致：运行头部 + 任务日志 + 完整报告。
func TestRunWritesLogFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(`{
  "targets": [
    {"name": "本地HTTP", "type": "http", "address": %q, "expected_status": 200}
  ]
}`, srv.URL))

	dir := t.TempDir()
	code, stdout, stderr := runAppWithLogDir(t, dir, cli.Options{
		ConfigPath: path,
		Timeout:    2 * time.Second,
		Verbose:    true,
	})

	if code != ExitOK {
		t.Fatalf("全部目标成功时退出码应为 0，实际为 %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("成功路径不应写 stderr，实际为: %s", stderr)
	}

	// 目录下应当正好有一个日志文件，文件名是 monitor-log- + 14 位时间戳 + .log。
	matches, err := filepath.Glob(filepath.Join(dir, "monitor-log-*.log"))
	if err != nil {
		t.Fatalf("扫描日志文件失败: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("应生成 1 个日志文件，实际为 %d 个: %v", len(matches), matches)
	}

	namePattern := regexp.MustCompile(`^monitor-log-\d{14}\.log$`)
	if name := filepath.Base(matches[0]); !namePattern.MatchString(name) {
		t.Errorf("日志文件名应符合 monitor-log-YYYYMMDDHHMMSS.log，实际为 %q", name)
	}

	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	text := string(content)

	for _, want := range []string{
		"# 服务健康探测运行日志",
		"# 配置文件: " + path,
		"# 单次超时: 2s",
		"[INFO] Starting service health checks...",
		"[RUNNING] 本地HTTP",
		"[SUCCESS] 本地HTTP",
		"========== Service Health Report ==========",
		"Succeeded     : 1",
		"[INFO] Report finished.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("日志文件缺少 %q\n文件内容:\n%s", want, text)
		}
	}

	// "日志文件: ..." 这行只给终端看，不应写进文件自身。
	if strings.Contains(text, "[INFO] 日志文件:") {
		t.Errorf("日志文件不应包含自身的路径提示\n文件内容:\n%s", text)
	}
	if !strings.Contains(stdout, "[INFO] 日志文件:") {
		t.Errorf("stdout 应提示日志文件路径\n实际输出:\n%s", stdout)
	}

	// 报告在文件中的位置同样要在任务日志之后。
	reportAt := strings.Index(text, "========== Service Health Report ==========")
	lastEventAt := strings.LastIndex(text, "[SUCCESS]")
	if reportAt < lastEventAt {
		t.Errorf("文件中的报告应位于任务日志之后\n文件内容:\n%s", text)
	}
}

// TestRunContinuesWhenLogFileCannotBeCreated 验证日志文件创建失败时降级为
// 仅终端输出：探测仍照常完成，退出码不变，只在 stderr 上留一条警告。
func TestRunContinuesWhenLogFileCannotBeCreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(`{
  "targets": [
    {"name": "本地HTTP", "type": "http", "address": %q}
  ]
}`, srv.URL))

	// 父目录存在但目标子目录不存在，Create 必然失败。
	missingDir := filepath.Join(t.TempDir(), "no-such-dir")
	code, stdout, stderr := runAppWithLogDir(t, missingDir, cli.Options{
		ConfigPath: path,
		Timeout:    2 * time.Second,
	})

	if code != ExitOK {
		t.Fatalf("日志文件不可写不应改变退出码，实际为 %d\nstdout:\n%s", code, stdout)
	}
	if !strings.Contains(stderr, "[WARN]") {
		t.Errorf("stderr 应包含降级警告，实际为: %s", stderr)
	}
	if !strings.Contains(stdout, "========== Service Health Report ==========") {
		t.Errorf("降级后仍应输出报告\n实际输出:\n%s", stdout)
	}
}
