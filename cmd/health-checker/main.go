// Command health-checker 是服务健康探测工具的可执行程序入口。
//
// 与模块根目录的 main.go 等价，两者都调用 internal/entry，区别只在构建方式：
// 本目录用于 `go build -o health-checker ./cmd/health-checker` 单独产出二进制。
package main

import (
	"os"

	"service-health-checker/internal/entry"
)

func main() {
	os.Exit(entry.Run(os.Args[1:], os.Stdout, os.Stderr))
}
