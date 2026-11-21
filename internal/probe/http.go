package probe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"service-health-checker/internal/model"
)

// HTTPProber 通过 HTTP GET 探测目标健康状况。
//
// 多个 Goroutine 可以并发复用同一个 HTTPProber：其内部只持有一个共享的
// *http.Client，而 http.Client 本身是并发安全的。
type HTTPProber struct {
	client *http.Client
}

// 编译期确认 HTTPProber 实现了 Prober。
var _ Prober = (*HTTPProber)(nil)

// defaultHTTPClient 是包级共享的 HTTP 客户端。
//
// 刻意不设置 http.Client.Timeout：本项目要求"每次探测独立超时"，超时必须由
// Executor 为每次尝试创建的 ctx 控制（doc 8.3.2）。若在此处再设置 Timeout，
// 会出现两套互相干扰的超时语义。
//
// 也不配置自定义 Transport / CheckRedirect / TLS 参数，保留标准库默认的重定向
// 跟随、证书校验与连接复用行为（doc 8.3.9）。
var defaultHTTPClient = &http.Client{}

// defaultUserAgent 是探测请求统一使用的 User-Agent。
//
// Go 标准库默认发送 "Go-http-client/1.1"，部分站点（实测 www.163.com）会针对该
// 特征直接返回 403，导致"服务本身健康、却判定为失败"。健康探测的目的是确认服务
// 是否可达，而不是复现某个具体客户端的身份，因此这里显式声明自身身份，避免被
// 按默认 UA 误杀。
//
// 若需要还原"默认客户端视角"的行为，删除下方 Probe 中的 req.Header.Set 一行即可。
const defaultUserAgent = "ServiceHealthChecker/1.0"

// NewHTTPProber 创建使用共享 http.Client 的探测器，正式运行路径应使用该构造函数。
func NewHTTPProber() *HTTPProber {
	return &HTTPProber{client: defaultHTTPClient}
}

// NewHTTPProberWithClient 使用调用方提供的 http.Client 创建探测器。
//
// 主要用于单元测试或后续需要替换客户端实现的场景；client 为 nil 时回退到共享客户端。
func NewHTTPProberWithClient(client *http.Client) *HTTPProber {
	if client == nil {
		return NewHTTPProber()
	}
	return &HTTPProber{client: client}
}

// Probe 发起一次 HTTP GET 探测。
//
// 判定顺序与 doc 8.3 一致：创建请求 -> 发起请求 -> 校验状态码 -> 校验正文包含。
// 任何一步失败都返回该步骤的独立错误，并尽量携带已获得的状态码。
func (p *HTTPProber) Probe(ctx context.Context, target model.Target) (Metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Address, nil)
	if err != nil {
		return Metadata{}, fmt.Errorf("创建 HTTP 请求失败（%s）: %w", target.Address, err)
	}
	// 显式声明 User-Agent，避免被按 Go 默认 UA 拦截（见 defaultUserAgent 注释）。
	// 判断后再赋值，为将来按目标定制 UA 留出扩展点。
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		// url.Error 会包装底层错误，ctx 超时/取消可通过 errors.Is 判断。
		return Metadata{}, fmt.Errorf("HTTP 请求失败（%s）: %w", target.Address, err)
	}
	// 无论后续校验是否通过，都必须关闭响应正文，避免连接与内存泄漏（doc 8.3.6）。
	defer resp.Body.Close()

	// 先记录状态码：即使后续判定失败，也要尽可能保留实际收到的状态码。
	metadata := Metadata{StatusCode: resp.StatusCode}

	if resp.StatusCode != target.ExpectedStatus {
		return metadata, fmt.Errorf("HTTP 状态码不符合预期：期望 %d，实际 %d", target.ExpectedStatus, resp.StatusCode)
	}

	if target.Contains == "" {
		return metadata, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return metadata, fmt.Errorf("读取响应正文失败: %w", err)
	}
	if !bytes.Contains(body, []byte(target.Contains)) {
		return metadata, fmt.Errorf("响应正文不包含期望内容 %q", target.Contains)
	}

	return metadata, nil
}
