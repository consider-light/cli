package probe

import (
	"context"
	"fmt"
	"net"

	"service-health-checker/internal/model"
)

// TCPProber 通过尝试建立 TCP 连接探测端口连通性。
//
// 它只验证能否完成 TCP 握手，不执行 MySQL/SSH 等协议级握手，也不校验账号与
// 服务内部状态（doc 1.4）。连接成功后立即关闭，不发送也不读取任何业务数据。
type TCPProber struct{}

// 编译期确认 TCPProber 实现了 Prober。
var _ Prober = (*TCPProber)(nil)

// NewTCPProber 创建 TCP 探测器。TCPProber 无可变状态，可被多个 Goroutine 并发复用。
func NewTCPProber() *TCPProber {
	return &TCPProber{}
}

// Probe 使用 DialContext 发起一次 TCP 连接尝试。
//
// 超时与取消完全由 ctx 控制：这里不设置 net.Dialer.Timeout，避免出现与 ctx
// 并存的第二套超时语义（doc 12.1）。
func (p *TCPProber) Probe(ctx context.Context, target model.Target) (Metadata, error) {
	// 地址合法性由配置层保证；这里再做一次轻量校验，避免把明显错误的地址
	// 交给 DialContext 后得到难以理解的错误。
	if _, _, err := net.SplitHostPort(target.Address); err != nil {
		return Metadata{}, fmt.Errorf("TCP 地址 %q 无效: %w", target.Address, err)
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", target.Address)
	if err != nil {
		return Metadata{}, fmt.Errorf("TCP 连接失败（%s）: %w", target.Address, err)
	}
	// 探测只关心能否连通，立刻关闭连接，避免占用服务端连接数。
	defer conn.Close()

	return Metadata{}, nil
}
