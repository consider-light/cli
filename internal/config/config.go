// Package config 负责加载、解析并校验 JSON 配置，把外部配置 DTO 转换为
// model.Target 列表。
//
// 该包只做"文件读取 + 反序列化 + 校验 + 默认值处理"，不启动 Goroutine、
// 不执行网络探测，也不关心并发调度与报告输出。这样可以保证非法配置在进入
// 调度层之前就被拦截。
package config

import "service-health-checker/internal/model"

const (
	// DefaultPath 是未指定 --config 时使用的配置文件路径。
	//
	// 该路径相对于进程的当前工作目录，而不是可执行文件所在目录。
	DefaultPath = "config.json"

	// defaultExpectedStatus 是 HTTP 目标未配置 expected_status 时使用的默认状态码。
	defaultExpectedStatus = 200

	// minHTTPStatus、maxHTTPStatus 是 expected_status 的合法区间。
	minHTTPStatus = 100
	maxHTTPStatus = 599

	// minRetryCount、maxRetryCount 是 retry_count 的合法区间。
	// retry_count 表示首次失败后的额外重试次数，0 表示不重试。
	minRetryCount = 0
	maxRetryCount = 3

	// minPort、maxPort 是 TCP 端口的合法区间。
	minPort = 1
	maxPort = 65535
)

// Config 是一次成功加载并校验后的运行配置。
type Config struct {
	// Source 配置来源的文件路径，仅用于日志与错误提示。
	Source string
	// Targets 已完成校验与默认值填充的目标列表，顺序与配置文件保持一致。
	Targets []model.Target
}

// fileConfig 是配置文件的顶层 JSON DTO。
type fileConfig struct {
	Targets []targetConfig `json:"targets"`
}

// targetConfig 是单个目标的 JSON DTO。
//
// 可选字段使用指针：nil 表示"字段缺省"，非 nil 表示"用户显式填写"。
// 这一区分是必要的——TCP 目标显式填写 expected_status 或 contains 属于非法配置，
// 若使用值类型就无法与缺省情况区分。
type targetConfig struct {
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	Address        string  `json:"address"`
	RetryCount     *int    `json:"retry_count"`
	ExpectedStatus *int    `json:"expected_status"`
	Contains       *string `json:"contains"`
}
