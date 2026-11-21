// Package model 定义探测目标、探测结果等项目核心数据结构。
//
// 该包不依赖项目内其他包，也不包含任何 I/O 操作，因此可被 config、probe、
// scheduler、collector、reporter 等模块共同复用，避免出现重复的 Target 定义
// 和 import cycle。
package model

// 支持的协议类型常量。
//
// 配置校验时会把用户填写的 type 统一转换为小写后比较，因此常量一律使用小写；
// 未识别出的类型不会被静默映射为某个有效类型，而是直接判定配置不合法。
const (
	// TypeHTTP 表示 HTTP/HTTPS 探测。
	TypeHTTP = "http"
	// TypeTCP 表示 TCP 端口连通性探测。
	TypeTCP = "tcp"
)

// Target 是一个"已通过配置校验"的探测目标，即内部运行模型。
//
// Target 不承担 JSON 反序列化职责：配置文件中 retry_count、expected_status、
// contains 等字段是可选的，需要用指针区分"字段缺省"与"用户显式填写了不适用
// 字段"，该职责由 config 包内部的 DTO 承担。进入 model.Target 时默认值已填充完毕，
// 因此下游模块无需再判断字段是否缺省。
type Target struct {
	// Name 服务名称，用于日志与报告展示。已去除首尾空格，保证非空。
	Name string
	// Type 协议类型，取值为 TypeHTTP 或 TypeTCP。
	Type string
	// Address HTTP 目标为完整 URL；TCP 目标为 host:port 形式的地址。
	Address string
	// RetryCount 首次探测失败后允许的额外重试次数，取值范围 0～3，缺省为 0。
	// 注意它不是总尝试次数：最大尝试次数为 RetryCount + 1。
	RetryCount int
	// ExpectedStatus HTTP 目标期望的状态码，缺省为 200；TCP 目标不使用，固定为 0。
	ExpectedStatus int
	// Contains HTTP 目标响应正文必须包含的字符串（大小写敏感的子串匹配）；
	// TCP 目标不使用，固定为空字符串。
	Contains string
}
