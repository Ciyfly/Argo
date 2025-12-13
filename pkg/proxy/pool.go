package proxy

import (
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// ProxyPool 代理池
type ProxyPool struct {
	proxies    []string
	current    uint64
	mu         sync.RWMutex
	failed     map[string]int
	maxFails   int
	lastRotate time.Time
}

// NewProxyPool 创建代理池
func NewProxyPool(proxies []string, maxFails int) *ProxyPool {
	if maxFails <= 0 {
		maxFails = 3
	}
	return &ProxyPool{
		proxies:    proxies,
		failed:     make(map[string]int),
		maxFails:   maxFails,
		lastRotate: time.Now(),
	}
}

// Size 返回代理池大小
func (p *ProxyPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.proxies)
}

// IsEmpty 检查代理池是否为空
func (p *ProxyPool) IsEmpty() bool {
	return p.Size() == 0
}

// Next 轮询获取下一个代理
func (p *ProxyPool) Next() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.proxies) == 0 {
		return ""
	}

	idx := atomic.AddUint64(&p.current, 1)
	return p.proxies[idx%uint64(len(p.proxies))]
}

// GetHealthy 获取一个健康的代理
func (p *ProxyPool) GetHealthy() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.proxies) == 0 {
		return ""
	}

	// 尝试找一个失败次数较少的代理
	for _, proxy := range p.proxies {
		if p.failed[proxy] < p.maxFails {
			return proxy
		}
	}

	// 所有代理都不健康，重置失败计数并返回第一个
	p.mu.RUnlock()
	p.mu.Lock()
	for k := range p.failed {
		delete(p.failed, k)
	}
	p.mu.Unlock()
	p.mu.RLock()

	return p.Next()
}

// MarkFailed 标记代理失败
func (p *ProxyPool) MarkFailed(proxy string) {
	if proxy == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.failed[proxy]++

	// 如果失败次数过多，从池中移除
	if p.failed[proxy] >= p.maxFails {
		p.removeProxyLocked(proxy)
	}
}

// MarkSuccess 标记代理成功，重置失败计数
func (p *ProxyPool) MarkSuccess(proxy string) {
	if proxy == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.failed, proxy)
}

// removeProxyLocked 从池中移除代理（需要持有锁）
func (p *ProxyPool) removeProxyLocked(proxy string) {
	newProxies := make([]string, 0, len(p.proxies)-1)
	for _, pr := range p.proxies {
		if pr != proxy {
			newProxies = append(newProxies, pr)
		}
	}
	p.proxies = newProxies
	delete(p.failed, proxy)
}

// Add 添加代理到池中
func (p *ProxyPool) Add(proxy string) {
	if proxy == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 检查是否已存在
	for _, pr := range p.proxies {
		if pr == proxy {
			return
		}
	}

	p.proxies = append(p.proxies, proxy)
}

// GetClient 获取带代理的 HTTP 客户端
func (p *ProxyPool) GetClient(timeout time.Duration) (*http.Client, string) {
	proxy := p.GetHealthy()
	if proxy == "" {
		return &http.Client{Timeout: timeout}, ""
	}

	proxyURL, err := url.Parse(proxy)
	if err != nil {
		return &http.Client{Timeout: timeout}, ""
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}, proxy
}

// Stats 返回代理池统计信息
type ProxyStats struct {
	Total   int            `json:"total"`
	Healthy int            `json:"healthy"`
	Failed  map[string]int `json:"failed"`
}

func (p *ProxyPool) Stats() ProxyStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	healthy := 0
	for _, proxy := range p.proxies {
		if p.failed[proxy] < p.maxFails {
			healthy++
		}
	}

	failedCopy := make(map[string]int)
	for k, v := range p.failed {
		failedCopy[k] = v
	}

	return ProxyStats{
		Total:   len(p.proxies),
		Healthy: healthy,
		Failed:  failedCopy,
	}
}

// ParseProxyList 解析代理列表字符串
// 支持格式: "http://proxy1:8080,http://proxy2:8080"
func ParseProxyList(proxyStr string) []string {
	if proxyStr == "" {
		return nil
	}

	var proxies []string
	parts := splitAndTrim(proxyStr, ",")
	for _, part := range parts {
		if part != "" {
			proxies = append(proxies, part)
		}
	}
	return proxies
}

func splitAndTrim(s string, sep string) []string {
	var result []string
	for _, part := range splitString(s, sep) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitString(s string, sep string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if i+len(sep) <= len(s) && s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
