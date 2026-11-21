// Package logfile 为每次运行生成落盘日志文件。
//
// 文件名固定为 monitor-log-YYYYMMDDHHMMSS.log，时间戳取自这次运行的开始时刻，
// 因此同一份配置连续跑两次会得到两个文件，便于对比。
//
// 文件内容与终端输出一致（终端看到什么，文件里就有什么），额外在开头多一段
// 运行信息：单独拿到一个 .log 时，也能判断是哪次运行、用的哪份配置。
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// Prefix 是日志文件名前缀。
	Prefix = "monitor-log-"
	// Ext 是日志文件名后缀。
	Ext = ".log"

	// timestampLayout 是文件名里的时间戳格式，固定 14 位：年月日时分秒。
	timestampLayout = "20060102150405"
	// headerLayout 是日志头部时间戳的展示格式，带分隔符便于人读。
	headerLayout = "2006-01-02 15:04:05"
)

// FileName 返回 startedAt 对应的日志文件名。
//
// 使用 startedAt 自身的时区，不做 UTC 转换：使用者看到的时间应该和系统时钟一致。
func FileName(startedAt time.Time) string {
	return Prefix + startedAt.Format(timestampLayout) + Ext
}

// File 是一次运行的日志文件。
type File struct {
	*os.File

	path string
}

// Path 返回日志文件的完整路径。
func (f *File) Path() string {
	return f.path
}

// Create 在 dir 下创建 startedAt 对应的日志文件。
//
// dir 为空时写入当前目录。文件以追加方式打开：正常使用不会撞名，但如果同一秒内
// 启动了两次，保留两次运行的内容比让后一次冲掉前一次更安全。
func Create(dir string, startedAt time.Time) (*File, error) {
	if dir == "" {
		dir = "."
	}

	path := filepath.Join(dir, FileName(startedAt))
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("创建日志文件 %s 失败: %w", path, err)
	}

	return &File{File: handle, path: path}, nil
}

// WriteHeader 写入本次运行的基本信息。
//
// 这段内容只出现在日志文件里，不进入终端：终端已有 INFO 行交代参数，而文件需要
// 自己说明身份，否则单独一个 .log 无法判断它属于哪次运行。
//
// 写入失败不影响探测结果，因此不返回错误，与 logger 的处理方式保持一致。
func (f *File) WriteHeader(startedAt time.Time, configPath string, timeout time.Duration) {
	_, _ = fmt.Fprintf(
		f,
		"# 服务健康探测运行日志\n# 开始时间: %s\n# 配置文件: %s\n# 单次超时: %s\n\n",
		startedAt.Format(headerLayout),
		configPath,
		timeout,
	)
}
