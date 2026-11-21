package probe

import (
	"errors"
	"fmt"
	"sync"

	"service-health-checker/internal/model"
)

// Registry 维护"协议类型 -> 探测器实例"的映射。
//
// Executor 在每次探测前通过 Get 获取探测器。为避免未来在运行时动态注册导致数据
// 竞争，内部使用读写锁保护映射；读取路径只持有读锁，代价可以忽略。
type Registry struct {
	mu      sync.RWMutex
	probers map[string]Prober
}

// NewRegistry 创建一个空注册表。正式运行通常使用 DefaultRegistry。
func NewRegistry() *Registry {
	return &Registry{probers: make(map[string]Prober, 2)}
}

// DefaultRegistry 返回预置了 http、tcp 探测器的注册表，供 App 层组装使用。
//
// 内建注册不可能失败（类型与探测器均为常量），失败属于编程错误，因此用
// MustRegister 让问题在启动阶段立即暴露，而不是推迟到探测时才 panic。
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.MustRegister(model.TypeHTTP, NewHTTPProber())
	r.MustRegister(model.TypeTCP, NewTCPProber())
	return r
}

// Register 注册一个协议的探测器。
//
// 空协议类型、nil 探测器、重复注册都会被拒绝：静默覆盖会让"配置里的 http
// 到底用了哪个实现"变得不可追踪。
func (r *Registry) Register(kind string, p Prober) error {
	if kind == "" {
		return errors.New("注册探测器失败：协议类型不能为空")
	}
	if p == nil {
		return fmt.Errorf("注册探测器失败：协议 %q 的探测器为 nil", kind)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.probers[kind]; exists {
		return fmt.Errorf("注册探测器失败：协议 %q 已注册探测器，不允许重复注册", kind)
	}
	r.probers[kind] = p
	return nil
}

// MustRegister 与 Register 行为一致，但注册失败时直接 panic。
// 仅用于内建探测器等"失败即编程错误"的场景。
func (r *Registry) MustRegister(kind string, p Prober) {
	if err := r.Register(kind, p); err != nil {
		panic(err)
	}
}

// Get 按协议类型返回探测器。
//
// 未知协议返回明确错误而不是 nil：调用方必须显式处理该错误，避免拿到 nil 后
// 继续执行导致空指针 panic（doc 8.2）。协议类型按精确值匹配，配置层已统一
// 转为小写，这里不做隐式转换，以免掩盖配置错误。
func (r *Registry) Get(kind string) (Prober, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.probers[kind]
	if !ok {
		return nil, fmt.Errorf("不支持的协议类型 %q", kind)
	}
	return p, nil
}

// Len 返回已注册的协议数量，主要用于测试与启动日志。
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.probers)
}
