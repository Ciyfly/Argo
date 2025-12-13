package proxy

import (
	"bufio"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProxyPool 代理池
type ProxyPool struct {
	proxies      []string
	current      uint64
	mu           sync.RWMutex
	failed       map[string]int
	success      map[string]int
	latency      map[string]int64 // 延迟毫秒
	maxFails     int
	lastRotate   time.Time
	healthClient *http.Client
	stopChan     chan struct{}
	rotation     string // round-robin, random, smart
}

// NewProxyPool 创建代理池
func NewProxyPool(proxies []string, maxFails int) *ProxyPool {
	if maxFails <= 0 {
		maxFails = 3
	}
	return &ProxyPool{
		proxies:      proxies,
		failed:       make(map[string]int),
		success:      make(map[string]int),
		latency:      make(map[string]int64),
		maxFails:     maxFails,
		lastRotate:   time.Now(),
		healthClient: &http.Client{Timeout: 10 * time.Second},
		stopChan:     make(chan struct{}),
		rotation:     "round-robin",
	}
}

// NewProxyPoolWithRotation 创建带轮换策略的代理池
func NewProxyPoolWithRotation(proxies []string, maxFails int, rotation string) *ProxyPool {
	pool := NewProxyPool(proxies, maxFails)
	if rotation != "" {
		pool.rotation = rotation
	}
	return pool
}

// LoadFromFile 从文件加载代理列表
func (p *ProxyPool) LoadFromFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	p.mu.Lock()
	defer p.mu.Unlock()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 确保有协议前缀
		if !strings.HasPrefix(line, "http://") && !strings.HasPrefix(line, "https://") && !strings.HasPrefix(line, "socks") {
			line = "http://" + line
		}

		// 验证 URL 格式
		if _, err := url.Parse(line); err != nil {
			continue
		}

		// 检查是否已存在
		exists := false
		for _, pr := range p.proxies {
			if pr == line {
				exists = true
				break
			}
		}
		if !exists {
			p.proxies = append(p.proxies, line)
		}
	}

	return scanner.Err()
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

// Get 根据配置的策略获取代理
func (p *ProxyPool) Get() string {
	switch p.rotation {
	case "smart":
		return p.GetSmart()
	case "random":
		return p.GetRandom()
	default:
		return p.GetHealthy()
	}
}

// GetSmart 智能获取代理 (基于成功率和延迟)
func (p *ProxyPool) GetSmart() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.proxies) == 0 {
		return ""
	}

	var bestProxy string
	var bestScore float64 = -1

	for _, proxy := range p.proxies {
		if p.failed[proxy] >= p.maxFails {
			continue
		}

		// 计算得分：成功率 * (1 - 延迟因子)
		total := p.success[proxy] + p.failed[proxy]
		if total == 0 {
			total = 1
		}
		successRate := float64(p.success[proxy]) / float64(total)

		// 延迟因子 (0-1, 延迟越高越接近1)
		latencyFactor := float64(p.latency[proxy]) / 10000.0
		if latencyFactor > 1 {
			latencyFactor = 1
		}

		score := successRate * (1 - latencyFactor*0.3)

		// 新代理给予初始分数
		if p.success[proxy] == 0 && p.failed[proxy] == 0 {
			score = 0.5
		}

		if score > bestScore {
			bestScore = score
			bestProxy = proxy
		}
	}

	if bestProxy != "" {
		return bestProxy
	}

	return p.Next()
}

// GetRandom 随机获取代理
func (p *ProxyPool) GetRandom() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.proxies) == 0 {
		return ""
	}

	// 收集健康代理
	healthy := make([]string, 0)
	for _, proxy := range p.proxies {
		if p.failed[proxy] < p.maxFails {
			healthy = append(healthy, proxy)
		}
	}

	if len(healthy) == 0 {
		return p.proxies[0]
	}

	// 简单随机选择
	idx := time.Now().UnixNano() % int64(len(healthy))
	return healthy[idx]
}

// MarkSuccessWithLatency 标记代理成功并记录延迟
func (p *ProxyPool) MarkSuccessWithLatency(proxy string, latency time.Duration) {
	if proxy == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.success[proxy]++
	p.failed[proxy] = 0
	p.latency[proxy] = latency.Milliseconds()
}

// StartHealthCheck 启动健康检查
func (p *ProxyPool) StartHealthCheck(healthCheckURL string, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	if healthCheckURL == "" {
		healthCheckURL = "https://www.google.com/generate_204"
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.checkAllProxies(healthCheckURL)
			case <-p.stopChan:
				return
			}
		}
	}()
}

// checkAllProxies 检查所有代理健康状态
func (p *ProxyPool) checkAllProxies(healthCheckURL string) {
	p.mu.RLock()
	proxies := make([]string, len(p.proxies))
	copy(proxies, p.proxies)
	p.mu.RUnlock()

	var wg sync.WaitGroup
	for _, proxy := range proxies {
		wg.Add(1)
		go func(proxyURL string) {
			defer wg.Done()
			p.checkProxy(proxyURL, healthCheckURL)
		}(proxy)
	}
	wg.Wait()
}

// checkProxy 检查单个代理健康状态
func (p *ProxyPool) checkProxy(proxyURL string, healthCheckURL string) {
	parsedProxy, err := url.Parse(proxyURL)
	if err != nil {
		p.MarkFailed(proxyURL)
		return
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(parsedProxy),
		},
	}

	start := time.Now()
	resp, err := client.Get(healthCheckURL)
	latency := time.Since(start)

	if err != nil {
		p.MarkFailed(proxyURL)
		return
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		p.MarkFailed(proxyURL)
		return
	}

	p.MarkSuccessWithLatency(proxyURL, latency)
}

// Stop 停止健康检查
func (p *ProxyPool) Stop() {
	select {
	case <-p.stopChan:
		// 已关闭
	default:
		close(p.stopChan)
	}
}

// HealthyCount 返回健康代理数量
func (p *ProxyPool) HealthyCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	count := 0
	for _, proxy := range p.proxies {
		if p.failed[proxy] < p.maxFails {
			count++
		}
	}
	return count
}

// SetRotation 设置轮换策略
func (p *ProxyPool) SetRotation(rotation string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rotation = rotation
}

// Clear 清空代理池
func (p *ProxyPool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.proxies = make([]string, 0)
	p.failed = make(map[string]int)
	p.success = make(map[string]int)
	p.latency = make(map[string]int64)
	atomic.StoreUint64(&p.current, 0)
}

// CreateTransport 创建带代理的 Transport
func (p *ProxyPool) CreateTransport() *http.Transport {
	return &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			proxyURL := p.Get()
			if proxyURL == "" {
				return nil, nil
			}
			return url.Parse(proxyURL)
		},
	}
}
