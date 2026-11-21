package probe

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"service-health-checker/internal/model"
)

// tcpTarget 构造 TCP 测试目标。
func tcpTarget(address string) model.Target {
	return model.Target{
		Name:    "test-tcp",
		Type:    model.TypeTCP,
		Address: address,
	}
}

// TestTCPProberSuccessAndClose 验证本地 Listener 可连通，且探测结束后连接被主动关闭。
func TestTCPProberSuccessAndClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动本地 Listener 失败: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	metadata, err := NewTCPProber().Probe(context.Background(), tcpTarget(ln.Addr().String()))
	if err != nil {
		t.Fatalf("本地端口可连通时应探测成功，实际错误: %v", err)
	}
	if metadata.StatusCode != 0 {
		t.Errorf("TCP 探测的 StatusCode 应为 0，实际为 %d", metadata.StatusCode)
	}

	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("服务端未能接受连接")
	}
	defer serverConn.Close()

	// 探测只验证连通性，返回前应关闭连接：服务端读取应立即得到 EOF 或连接重置。
	_ = serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := serverConn.Read(make([]byte, 1)); err == nil {
		t.Error("探测结束后连接仍可读取数据，说明客户端未关闭连接")
	}
}

// TestTCPProberConnectionRefused 验证端口未监听时探测失败。
func TestTCPProberConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动本地 Listener 失败: %v", err)
	}
	address := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("关闭 Listener 失败: %v", err)
	}

	_, err = NewTCPProber().Probe(context.Background(), tcpTarget(address))
	if err == nil {
		t.Fatal("端口未监听时应探测失败")
	}
	if !strings.Contains(err.Error(), "TCP 连接失败") {
		t.Errorf("错误信息应说明连接失败，实际为: %v", err)
	}
}

// TestTCPProberCanceledContext 验证 ctx 取消后能立即结束，不会长时间阻塞。
func TestTCPProberCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if _, err := NewTCPProber().Probe(ctx, tcpTarget("192.0.2.1:81")); err == nil {
		t.Fatal("ctx 已取消时应探测失败")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("ctx 已取消时应立即返回，实际耗时 %v", elapsed)
	}
}

// TestTCPProberDialTimeout 验证连接超时由 ctx deadline 控制，能及时结束。
// 192.0.2.0/24 是文档保留地址段（TEST-NET-1），不会真实连上；部分网络会直接
// 返回不可达错误，因此这里只断言"在限期内返回错误"。
func TestTCPProberDialTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewTCPProber().Probe(ctx, tcpTarget("192.0.2.1:81"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("连接不可达地址时应探测失败")
	}
	if elapsed > 3*time.Second {
		t.Errorf("探测应在超时后及时结束，实际耗时 %v", elapsed)
	}
}

func TestTCPProberInvalidAddress(t *testing.T) {
	_, err := NewTCPProber().Probe(context.Background(), tcpTarget("localhost"))
	if err == nil {
		t.Fatal("缺少端口的地址应返回错误")
	}
	if !strings.Contains(err.Error(), "无效") {
		t.Errorf("错误信息应说明地址无效，实际为: %v", err)
	}
}
