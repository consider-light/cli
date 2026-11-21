package probe

import (
	"context"
	"sync"
	"testing"

	"service-health-checker/internal/model"
)

// stubProber 是注册表测试用的假探测器，不产生任何网络行为。
type stubProber struct {
	metadata Metadata
	err      error
}

func (s stubProber) Probe(context.Context, model.Target) (Metadata, error) {
	return s.metadata, s.err
}

func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()

	if got := r.Len(); got != 2 {
		t.Errorf("默认注册表应包含 2 个探测器，实际为 %d", got)
	}

	httpProber, err := r.Get(model.TypeHTTP)
	if err != nil {
		t.Fatalf("获取 http 探测器失败: %v", err)
	}
	if _, ok := httpProber.(*HTTPProber); !ok {
		t.Errorf("http 探测器类型应为 *HTTPProber，实际为 %T", httpProber)
	}

	tcpProber, err := r.Get(model.TypeTCP)
	if err != nil {
		t.Fatalf("获取 tcp 探测器失败: %v", err)
	}
	if _, ok := tcpProber.(*TCPProber); !ok {
		t.Errorf("tcp 探测器类型应为 *TCPProber，实际为 %T", tcpProber)
	}
}

func TestRegistryGetUnknownProtocol(t *testing.T) {
	r := DefaultRegistry()

	p, err := r.Get("ftp")
	if err == nil {
		t.Fatal("未知协议应返回错误")
	}
	if p != nil {
		t.Errorf("未知协议不应返回探测器，实际返回 %T", p)
	}
}

func TestRegistryGetEmptyProtocol(t *testing.T) {
	if _, err := NewRegistry().Get(""); err == nil {
		t.Fatal("空协议类型应返回错误")
	}
}

func TestRegistryRegisterErrors(t *testing.T) {
	tests := []struct {
		name string
		kind string
		p    Prober
	}{
		{name: "空协议类型", kind: "", p: stubProber{}},
		{name: "nil 探测器", kind: "custom", p: nil},
		{name: "重复注册 http", kind: model.TypeHTTP, p: stubProber{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := DefaultRegistry()
			if err := r.Register(tc.kind, tc.p); err == nil {
				t.Fatalf("期望注册失败，实际成功（kind=%q）", tc.kind)
			}
		})
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	want := stubProber{metadata: Metadata{StatusCode: 204}}

	if err := r.Register("custom", want); err != nil {
		t.Fatalf("注册自定义探测器失败: %v", err)
	}

	got, err := r.Get("custom")
	if err != nil {
		t.Fatalf("获取自定义探测器失败: %v", err)
	}
	metadata, err := got.Probe(context.Background(), model.Target{})
	if err != nil {
		t.Fatalf("调用自定义探测器失败: %v", err)
	}
	if metadata.StatusCode != 204 {
		t.Errorf("返回的探测器实例不正确，StatusCode 应为 204，实际为 %d", metadata.StatusCode)
	}
}

// TestRegistryMustRegisterPanics 验证内建注册失败会立即暴露（用于 DefaultRegistry）。
func TestRegistryMustRegisterPanics(t *testing.T) {
	r := NewRegistry()
	r.MustRegister("dup", stubProber{})

	defer func() {
		if recover() == nil {
			t.Error("重复注册时 MustRegister 应 panic")
		}
	}()
	r.MustRegister("dup", stubProber{})
}

// TestRegistryConcurrentGet 在 -race 下验证并发读取注册表不存在数据竞争。
func TestRegistryConcurrentGet(t *testing.T) {
	r := DefaultRegistry()

	const goroutines = 8
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if _, err := r.Get(model.TypeHTTP); err != nil {
					t.Errorf("并发获取探测器失败: %v", err)
					return
				}
				if _, err := r.Get(model.TypeTCP); err != nil {
					t.Errorf("并发获取探测器失败: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
