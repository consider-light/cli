package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"service-health-checker/internal/model"
)

// writeTempConfig 在测试临时目录写入配置文件并返回路径。
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeTempConfig(t, `{
  "targets": [
    {"name": "百度搜索", "type": "http", "address": "https://www.baidu.com", "expected_status": 200},
    {"name": "MySQL服务", "type": "tcp", "address": "localhost:3306", "retry_count": 2}
  ]
}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load 返回意外错误: %v", err)
	}
	if cfg.Source != path {
		t.Errorf("Source 应为 %q，实际为 %q", path, cfg.Source)
	}
	if len(cfg.Targets) != 2 {
		t.Fatalf("目标数量应为 2，实际为 %d", len(cfg.Targets))
	}
	if cfg.Targets[0].Name != "百度搜索" || cfg.Targets[0].ExpectedStatus != 200 {
		t.Errorf("第一个目标解析不符合预期: %#v", cfg.Targets[0])
	}
	if cfg.Targets[1].Type != model.TypeTCP || cfg.Targets[1].RetryCount != 2 {
		t.Errorf("第二个目标解析不符合预期: %#v", cfg.Targets[1])
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-exist.json")

	cfg, err := Load(path)
	if err == nil {
		t.Fatalf("期望返回错误，实际解析成功: %#v", cfg)
	}
	for _, want := range []string{"读取配置文件", "not-exist.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息未包含 %q\n实际错误: %s", want, err.Error())
		}
	}
}

func TestLoadInvalidContent(t *testing.T) {
	path := writeTempConfig(t, `{"targets": [}`)

	cfg, err := Load(path)
	if err == nil {
		t.Fatalf("期望返回错误，实际解析成功: %#v", cfg)
	}
	for _, want := range []string{"配置文件", "无效", "JSON"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息未包含 %q\n实际错误: %s", want, err.Error())
		}
	}
}

// TestLoadWithUTF8BOM 覆盖 Windows 编辑器可能写入的 UTF-8 BOM 场景。
func TestLoadWithUTF8BOM(t *testing.T) {
	body := `{"targets":[{"name":"A","type":"http","address":"https://example.com"}]}`
	path := writeTempConfig(t, "\ufeff"+body)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("带 BOM 的配置应能正常加载，实际错误: %v", err)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Name != "A" {
		t.Fatalf("解析结果不符合预期: %#v", cfg.Targets)
	}
}

// TestLoadProjectConfigFile 校验仓库根目录随项目交付的 config.json 自身合法。
// 该测试不发起网络请求，只读取本地文件。
func TestLoadProjectConfigFile(t *testing.T) {
	path := filepath.Join("..", "..", DefaultPath)

	if _, err := os.Stat(path); err != nil {
		t.Skipf("跳过：未找到项目根目录的 %s (%v)", DefaultPath, err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("项目根目录 %s 应能通过校验，实际错误: %v", DefaultPath, err)
	}
	if len(cfg.Targets) != 9 {
		t.Fatalf("配置应包含 9 个目标，实际为 %d", len(cfg.Targets))
	}

	byName := make(map[string]model.Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		byName[target.Name] = target
	}

	for _, name := range []string{"百度搜索", "网易首页", "GitHub", "MySQL服务", "SSH服务", "无效服务", "B站API", "Raw文本文件", "慢速响应模拟"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("配置缺少目标 %q", name)
		}
	}

	if got := byName["GitHub"].RetryCount; got != 1 {
		t.Errorf("GitHub 的 retry_count 应为 1，实际为 %d", got)
	}
	if got := byName["Raw文本文件"].Contains; got != "Go" {
		t.Errorf("Raw文本文件 的 contains 应为 %q，实际为 %q", "Go", got)
	}
	if got := byName["MySQL服务"].Type; got != model.TypeTCP {
		t.Errorf("MySQL服务 的 type 应为 %q，实际为 %q", model.TypeTCP, got)
	}
	if got := byName["慢速响应模拟"].ExpectedStatus; got != 200 {
		t.Errorf("慢速响应模拟 的 expected_status 应为 200，实际为 %d", got)
	}
}
