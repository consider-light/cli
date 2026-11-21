package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedTime 是测试用的固定时刻，避免断言依赖真实时钟。
func fixedTime() time.Time {
	return time.Date(2026, 11, 25, 16, 40, 40, 0, time.UTC)
}

func TestFileNameFormat(t *testing.T) {
	got := FileName(fixedTime())
	want := "monitor-log-20261125164040.log"

	if got != want {
		t.Errorf("日志文件名应为 %q，实际为 %q", want, got)
	}
}

// TestFileNameUsesLocalClock 验证文件名里的时间来自传入时刻本身，不做时区转换：
// 使用者看到的时间应当与系统时钟一致，而不是被默默换成 UTC。
func TestFileNameUsesLocalClock(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	got := FileName(time.Date(2026, 11, 25, 16, 40, 40, 0, loc))

	if want := "monitor-log-20261125164040.log"; got != want {
		t.Errorf("文件名应为 %q，实际为 %q", want, got)
	}
}

func TestCreateInDirectory(t *testing.T) {
	dir := t.TempDir()

	file, err := Create(dir, fixedTime())
	if err != nil {
		t.Fatalf("创建日志文件失败: %v", err)
	}
	defer file.Close()

	wantPath := filepath.Join(dir, "monitor-log-20261125164040.log")
	if file.Path() != wantPath {
		t.Errorf("路径应为 %q，实际为 %q", wantPath, file.Path())
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("日志文件未落盘: %v", err)
	}
}

// TestCreatedFileIsWritable 验证返回的文件可以直接写入，且内容能被读回。
func TestCreatedFileIsWritable(t *testing.T) {
	dir := t.TempDir()

	file, err := Create(dir, fixedTime())
	if err != nil {
		t.Fatalf("创建日志文件失败: %v", err)
	}

	if _, err := file.WriteString("report line\n"); err != nil {
		t.Fatalf("写入日志失败: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("关闭日志文件失败: %v", err)
	}

	content, err := os.ReadFile(file.Path())
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	if string(content) != "report line\n" {
		t.Errorf("日志内容应为 %q，实际为 %q", "report line\n", content)
	}
}

// TestWriteHeader 验证头部包含运行时间、配置路径与超时，且以空行结束，
// 保证后续日志与头部之间有视觉分隔。
func TestWriteHeader(t *testing.T) {
	dir := t.TempDir()

	file, err := Create(dir, fixedTime())
	if err != nil {
		t.Fatalf("创建日志文件失败: %v", err)
	}
	file.WriteHeader(fixedTime(), "config.json", 3*time.Second)
	if err := file.Close(); err != nil {
		t.Fatalf("关闭日志文件失败: %v", err)
	}

	content, err := os.ReadFile(file.Path())
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}

	text := string(content)
	for _, want := range []string{
		"# 服务健康探测运行日志",
		"# 开始时间: 2026-11-25 16:40:40",
		"# 配置文件: config.json",
		"# 单次超时: 3s",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("头部缺少 %q\n实际内容:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(text, "\n\n") {
		t.Errorf("头部应以空行结束，实际内容:\n%q", text)
	}
}

// TestCreateFailsOnMissingDirectory 验证目录不存在时返回错误而不是 panic，
// 由调用方决定降级策略。
func TestCreateFailsOnMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")

	file, err := Create(missing, fixedTime())
	if err == nil {
		file.Close()
		t.Fatal("目录不存在时应返回错误，实际为 nil")
	}
	if !strings.Contains(err.Error(), "创建日志文件") {
		t.Errorf("错误信息应说明创建日志文件失败，实际为: %v", err)
	}
}

// TestCreateWithEmptyDirUsesCurrentDirectory 验证空目录参数落到当前工作目录。
func TestCreateWithEmptyDirUsesCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	file, err := Create("", fixedTime())
	if err != nil {
		t.Fatalf("创建日志文件失败: %v", err)
	}
	defer file.Close()

	if want := "monitor-log-20261125164040.log"; file.Path() != want {
		t.Errorf("路径应为 %q，实际为 %q", want, file.Path())
	}
	if _, err := os.Stat(filepath.Join(dir, "monitor-log-20261125164040.log")); err != nil {
		t.Fatalf("文件应落在当前目录: %v", err)
	}
}

// TestCreateAppendsWhenNameRepeats 验证同一秒重复创建不会截断已有内容。
// 正常情况下不会撞名，但一旦发生，丢日志比多几条日志更糟。
func TestCreateAppendsWhenNameRepeats(t *testing.T) {
	dir := t.TempDir()

	first, err := Create(dir, fixedTime())
	if err != nil {
		t.Fatalf("第一次创建失败: %v", err)
	}
	if _, err := first.WriteString("first run\n"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	second, err := Create(dir, fixedTime())
	if err != nil {
		t.Fatalf("第二次创建失败: %v", err)
	}
	if _, err := second.WriteString("second run\n"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	content, err := os.ReadFile(second.Path())
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	if string(content) != "first run\nsecond run\n" {
		t.Errorf("两次运行内容应同时保留，实际为 %q", content)
	}
}
