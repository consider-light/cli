package config

import (
	"reflect"
	"strings"
	"testing"

	"service-health-checker/internal/model"
)

// TestParseValid 覆盖合法配置的解析与默认值填充。
func TestParseValid(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []model.Target
	}{
		{
			name: "HTTP 目标只填必填字段时使用默认值",
			json: `{"targets":[{"name":"站点","type":"http","address":"https://example.com"}]}`,
			want: []model.Target{{
				Name:           "站点",
				Type:           model.TypeHTTP,
				Address:        "https://example.com",
				RetryCount:     0,
				ExpectedStatus: 200,
				Contains:       "",
			}},
		},
		{
			name: "HTTP 目标显式配置 retry_count、expected_status 与 contains",
			json: `{"targets":[{"name":"A","type":"http","address":"http://127.0.0.1:8080/health",` +
				`"retry_count":3,"expected_status":204,"contains":"ok"}]}`,
			want: []model.Target{{
				Name:           "A",
				Type:           model.TypeHTTP,
				Address:        "http://127.0.0.1:8080/health",
				RetryCount:     3,
				ExpectedStatus: 204,
				Contains:       "ok",
			}},
		},
		{
			name: "retry_count 为 0 表示不重试",
			json: `{"targets":[{"name":"A","type":"http","address":"https://example.com","retry_count":0}]}`,
			want: []model.Target{{
				Name:           "A",
				Type:           model.TypeHTTP,
				Address:        "https://example.com",
				RetryCount:     0,
				ExpectedStatus: 200,
			}},
		},
		{
			name: "expected_status 显式填 0 视为缺省并取 200",
			json: `{"targets":[{"name":"A","type":"http","address":"https://example.com","expected_status":0}]}`,
			want: []model.Target{{
				Name:           "A",
				Type:           model.TypeHTTP,
				Address:        "https://example.com",
				ExpectedStatus: 200,
			}},
		},
		{
			name: "TCP 目标不填 HTTP 字段时 HTTP 相关字段为零值",
			json: `{"targets":[{"name":"MySQL服务","type":"tcp","address":"localhost:3306"}]}`,
			want: []model.Target{{
				Name:           "MySQL服务",
				Type:           model.TypeTCP,
				Address:        "localhost:3306",
				RetryCount:     0,
				ExpectedStatus: 0,
				Contains:       "",
			}},
		},
		{
			name: "TCP 目标支持 IPv6 方括号地址",
			json: `{"targets":[{"name":"IPv6","type":"tcp","address":"[::1]:3306"}]}`,
			want: []model.Target{{
				Name:    "IPv6",
				Type:    model.TypeTCP,
				Address: "[::1]:3306",
			}},
		},
		{
			name: "type 大写会被统一为小写",
			json: `{"targets":[{"name":"A","type":"HTTP","address":"https://example.com"}]}`,
			want: []model.Target{{
				Name:           "A",
				Type:           model.TypeHTTP,
				Address:        "https://example.com",
				ExpectedStatus: 200,
			}},
		},
		{
			name: "name 与 address 首尾空格会被裁剪",
			json: `{"targets":[{"name":"  A  ","type":" tcp ","address":" localhost:3306 "}]}`,
			want: []model.Target{{
				Name:    "A",
				Type:    model.TypeTCP,
				Address: "localhost:3306",
			}},
		},
		{
			name: "多目标保持配置顺序",
			json: `{"targets":[` +
				`{"name":"first","type":"http","address":"https://a.example.com"},` +
				`{"name":"second","type":"tcp","address":"localhost:22"}]}`,
			want: []model.Target{
				{Name: "first", Type: model.TypeHTTP, Address: "https://a.example.com", ExpectedStatus: 200},
				{Name: "second", Type: model.TypeTCP, Address: "localhost:22"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.json))
			if err != nil {
				t.Fatalf("Parse 返回意外错误: %v", err)
			}
			if !reflect.DeepEqual(cfg.Targets, tc.want) {
				t.Fatalf("解析结果不符合预期\n实际: %#v\n期望: %#v", cfg.Targets, tc.want)
			}
		})
	}
}

// TestParseInvalid 覆盖各类非法配置，要求返回可读错误并指出定位信息。
func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		wantContain []string
	}{
		{
			name:        "JSON 语法错误",
			json:        `{"targets":[`,
			wantContain: []string{"JSON 格式错误"},
		},
		{
			name:        "配置内容为空",
			json:        ``,
			wantContain: []string{"配置内容为空"},
		},
		{
			name:        "顶层为 null",
			json:        `null`,
			wantContain: []string{"targets 不能为空"},
		},
		{
			name:        "缺少 targets 字段",
			json:        `{}`,
			wantContain: []string{"targets 不能为空"},
		},
		{
			name:        "targets 为空数组",
			json:        `{"targets":[]}`,
			wantContain: []string{"targets 不能为空"},
		},
		{
			name:        "targets 类型错误",
			json:        `{"targets":{}}`,
			wantContain: []string{"JSON 字段 targets", "类型不匹配"},
		},
		{
			name:        "缺少 name",
			json:        `{"targets":[{"type":"http","address":"https://example.com"}]}`,
			wantContain: []string{"targets[0]", "字段 name", "不能为空"},
		},
		{
			name:        "name 为空白字符",
			json:        `{"targets":[{"name":"   ","type":"http","address":"https://example.com"}]}`,
			wantContain: []string{"字段 name", "不能为空"},
		},
		{
			name:        "缺少 type",
			json:        `{"targets":[{"name":"A","address":"https://example.com"}]}`,
			wantContain: []string{"字段 type", "不能为空"},
		},
		{
			name:        "缺少 address",
			json:        `{"targets":[{"name":"A","type":"http"}]}`,
			wantContain: []string{"字段 address", "不能为空"},
		},
		{
			name:        "未知协议不被静默映射",
			json:        `{"targets":[{"name":"A","type":"ftp","address":"ftp://example.com"}]}`,
			wantContain: []string{"字段 type", `不支持协议 "ftp"`},
		},
		{
			name:        "retry_count 为负数",
			json:        `{"targets":[{"name":"A","type":"http","address":"https://example.com","retry_count":-1}]}`,
			wantContain: []string{"字段 retry_count", "0～3"},
		},
		{
			name:        "retry_count 大于 3",
			json:        `{"targets":[{"name":"A","type":"http","address":"https://example.com","retry_count":4}]}`,
			wantContain: []string{"字段 retry_count", "0～3"},
		},
		{
			name:        "retry_count 为字符串",
			json:        `{"targets":[{"name":"A","type":"http","address":"https://example.com","retry_count":"2"}]}`,
			wantContain: []string{"retry_count", "类型不匹配"},
		},
		{
			name:        "HTTP 地址缺少 scheme",
			json:        `{"targets":[{"name":"A","type":"http","address":"www.example.com"}]}`,
			wantContain: []string{"字段 address", "scheme"},
		},
		{
			name:        "HTTP 地址 scheme 不受支持",
			json:        `{"targets":[{"name":"A","type":"http","address":"ftp://example.com"}]}`,
			wantContain: []string{"字段 address", "scheme"},
		},
		{
			name:        "HTTP 地址缺少主机名",
			json:        `{"targets":[{"name":"A","type":"http","address":"http:///path"}]}`,
			wantContain: []string{"字段 address", "主机名"},
		},
		{
			name:        "expected_status 小于 100",
			json:        `{"targets":[{"name":"A","type":"http","address":"https://example.com","expected_status":99}]}`,
			wantContain: []string{"字段 expected_status", "100～599"},
		},
		{
			name:        "expected_status 大于 599",
			json:        `{"targets":[{"name":"A","type":"http","address":"https://example.com","expected_status":600}]}`,
			wantContain: []string{"字段 expected_status", "100～599"},
		},
		{
			name:        "TCP 地址缺少端口",
			json:        `{"targets":[{"name":"A","type":"tcp","address":"localhost"}]}`,
			wantContain: []string{"字段 address", "host:port"},
		},
		{
			name:        "TCP 地址缺少主机",
			json:        `{"targets":[{"name":"A","type":"tcp","address":":3306"}]}`,
			wantContain: []string{"字段 address", "缺少主机地址"},
		},
		{
			name:        "TCP 端口为 0",
			json:        `{"targets":[{"name":"A","type":"tcp","address":"localhost:0"}]}`,
			wantContain: []string{"字段 address", "端口"},
		},
		{
			name:        "TCP 端口超出范围",
			json:        `{"targets":[{"name":"A","type":"tcp","address":"localhost:70000"}]}`,
			wantContain: []string{"字段 address", "端口"},
		},
		{
			name:        "TCP 端口不是整数",
			json:        `{"targets":[{"name":"A","type":"tcp","address":"localhost:abc"}]}`,
			wantContain: []string{"字段 address", "不是合法的整数"},
		},
		{
			name:        "TCP 目标配置 expected_status",
			json:        `{"targets":[{"name":"A","type":"tcp","address":"localhost:3306","expected_status":200}]}`,
			wantContain: []string{"字段 expected_status", "不适用于 TCP"},
		},
		{
			name:        "TCP 目标配置 contains",
			json:        `{"targets":[{"name":"A","type":"tcp","address":"localhost:3306","contains":"Go"}]}`,
			wantContain: []string{"字段 contains", "不适用于 TCP"},
		},
		{
			name: "多个目标的问题会被一次性汇总",
			json: `{"targets":[` +
				`{"name":"bad1","type":"http","address":"https://example.com","retry_count":9},` +
				`{"name":"bad2","type":"tcp","address":"localhost"}]}`,
			wantContain: []string{"共 2 处问题", "targets[0]（bad1）", "targets[1]（bad2）"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.json))
			if err == nil {
				t.Fatalf("期望返回配置错误，实际解析成功: %#v", cfg)
			}
			msg := err.Error()
			for _, want := range tc.wantContain {
				if !strings.Contains(msg, want) {
					t.Errorf("错误信息未包含 %q\n实际错误: %s", want, msg)
				}
			}
		})
	}
}

// TestParseInvalidReportsIndexAndName 校验错误必须包含数组索引与目标名称，便于用户定位。
func TestParseInvalidReportsIndexAndName(t *testing.T) {
	_, err := Parse([]byte(`{"targets":[` +
		`{"name":"ok","type":"http","address":"https://example.com"},` +
		`{"name":"坏目标","type":"tcp","address":"localhost:99999"}]}`))
	if err == nil {
		t.Fatal("期望返回配置错误，实际为 nil")
	}
	for _, want := range []string{"targets[1]", "坏目标", "address"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息未包含 %q\n实际错误: %s", want, err.Error())
		}
	}
}
