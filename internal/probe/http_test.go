package probe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"service-health-checker/internal/model"
)

// newHTTPTestServer 启动一个本地测试服务，覆盖成功、状态码不匹配、慢响应、
// 重定向等场景，避免单元测试依赖公共网络。
func newHTTPTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "hello Go world")
		case "/notfound":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "no such page")
		case "/slow":
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// probeTarget 构造 HTTP 测试目标，默认期望 200。
func probeTarget(address string, contains string) model.Target {
	return model.Target{
		Name:           "test-http",
		Type:           model.TypeHTTP,
		Address:        address,
		ExpectedStatus: http.StatusOK,
		Contains:       contains,
	}
}

func TestHTTPProberStatusOK(t *testing.T) {
	srv := newHTTPTestServer(t)

	metadata, err := NewHTTPProber().Probe(context.Background(), probeTarget(srv.URL+"/ok", ""))
	if err != nil {
		t.Fatalf("期望探测成功，实际错误: %v", err)
	}
	if metadata.StatusCode != http.StatusOK {
		t.Errorf("StatusCode 应为 200，实际为 %d", metadata.StatusCode)
	}
}

func TestHTTPProberStatusMismatch(t *testing.T) {
	srv := newHTTPTestServer(t)

	metadata, err := NewHTTPProber().Probe(context.Background(), probeTarget(srv.URL+"/notfound", ""))
	if err == nil {
		t.Fatal("期望探测失败，实际成功")
	}
	if metadata.StatusCode != http.StatusNotFound {
		t.Errorf("失败时仍应保留实际状态码 404，实际为 %d", metadata.StatusCode)
	}
	if !strings.Contains(err.Error(), "状态码") {
		t.Errorf("错误信息应说明状态码问题，实际为: %v", err)
	}
}

func TestHTTPProberContainsMatch(t *testing.T) {
	srv := newHTTPTestServer(t)

	metadata, err := NewHTTPProber().Probe(context.Background(), probeTarget(srv.URL+"/ok", "Go"))
	if err != nil {
		t.Fatalf("正文包含期望内容时应探测成功，实际错误: %v", err)
	}
	if metadata.StatusCode != http.StatusOK {
		t.Errorf("StatusCode 应为 200，实际为 %d", metadata.StatusCode)
	}
}

func TestHTTPProberContainsMismatch(t *testing.T) {
	srv := newHTTPTestServer(t)

	metadata, err := NewHTTPProber().Probe(context.Background(), probeTarget(srv.URL+"/ok", "Java"))
	if err == nil {
		t.Fatal("正文不包含期望内容时应探测失败")
	}
	if metadata.StatusCode != http.StatusOK {
		t.Errorf("正文校验失败时仍应保留状态码 200，实际为 %d", metadata.StatusCode)
	}
	if !strings.Contains(err.Error(), "不包含") {
		t.Errorf("错误信息应说明正文不匹配，实际为: %v", err)
	}
}

// TestHTTPProberTimeout 验证单次探测超时由传入 ctx 控制，而不是 http.Client.Timeout。
func TestHTTPProberTimeout(t *testing.T) {
	srv := newHTTPTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewHTTPProber().Probe(ctx, probeTarget(srv.URL+"/slow", ""))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("服务端响应超过超时时间时应探测失败")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("错误应可由 errors.Is 判定为 DeadlineExceeded，实际为: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("超时应在 50ms 附近生效，实际耗时 %v", elapsed)
	}
}

// TestHTTPProberRedirect 验证默认跟随重定向，并以最终响应作为探测结果。
func TestHTTPProberRedirect(t *testing.T) {
	srv := newHTTPTestServer(t)

	metadata, err := NewHTTPProber().Probe(context.Background(), probeTarget(srv.URL+"/redirect", ""))
	if err != nil {
		t.Fatalf("跟随重定向后状态码为 200，应探测成功，实际错误: %v", err)
	}
	if metadata.StatusCode != http.StatusOK {
		t.Errorf("应以最终响应状态码 200 为准，实际为 %d", metadata.StatusCode)
	}
}

func TestHTTPProberCanceledContext(t *testing.T) {
	srv := newHTTPTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewHTTPProber().Probe(ctx, probeTarget(srv.URL+"/ok", "")); err == nil {
		t.Fatal("ctx 已取消时应探测失败")
	}
}

func TestHTTPProberInvalidURL(t *testing.T) {
	_, err := NewHTTPProber().Probe(context.Background(), probeTarget("http://exa mple.com", ""))
	if err == nil {
		t.Fatal("非法 URL 应返回错误")
	}
	if !strings.Contains(err.Error(), "创建 HTTP 请求失败") {
		t.Errorf("错误信息应说明请求创建失败，实际为: %v", err)
	}
}

func TestHTTPProberConnectionFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	address := srv.URL
	srv.Close()

	_, err := NewHTTPProber().Probe(context.Background(), probeTarget(address+"/ok", ""))
	if err == nil {
		t.Fatal("服务端已关闭时应探测失败")
	}
	if !strings.Contains(err.Error(), "HTTP 请求失败") {
		t.Errorf("错误信息应说明网络请求失败，实际为: %v", err)
	}
}

// TestHTTPProberWithNilClient 确保构造函数对 nil 客户端做了兜底，不会在探测时 panic。
func TestHTTPProberWithNilClient(t *testing.T) {
	srv := newHTTPTestServer(t)

	p := NewHTTPProberWithClient(nil)
	if p == nil {
		t.Fatal("构造函数不应返回 nil")
	}
	if _, err := p.Probe(context.Background(), probeTarget(srv.URL+"/ok", "")); err != nil {
		t.Fatalf("使用兜底客户端应能正常探测，实际错误: %v", err)
	}
}

// TestHTTPProberSendsDefaultUserAgent 验证探测请求使用显式声明的 UA，
// 而不是 Go 标准库默认的 "Go-http-client/1.1"。
//
// 背景：实测 https://www.163.com 对 Go 默认 UA 直接返回 403，会导致
// "服务健康却被判定失败"。该用例锁住这一行为，避免以后被改回默认值。
func TestHTTPProberSendsDefaultUserAgent(t *testing.T) {
	// 用带缓冲的 channel 传递，避免 handler 与测试主 goroutine 之间的数据竞争。
	uaCh := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uaCh <- r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	if _, err := NewHTTPProber().Probe(context.Background(), probeTarget(srv.URL, "")); err != nil {
		t.Fatalf("探测不应失败，实际错误: %v", err)
	}

	gotUA := <-uaCh
	if gotUA != defaultUserAgent {
		t.Errorf("User-Agent 应为 %q，实际为 %q", defaultUserAgent, gotUA)
	}
	if strings.Contains(gotUA, "Go-http-client") {
		t.Errorf("不应发送 Go 默认 UA，实际为 %q", gotUA)
	}
}
