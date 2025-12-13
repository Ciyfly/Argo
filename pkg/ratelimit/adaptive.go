package ratelimit

import (
	"sync"
	"time"
)

// AdaptiveRateLimiter 自适应限速器
type AdaptiveRateLimiter struct {
	mu              sync.Mutex
	baseInterval    time.Duration // 基准间隔
	currentInterval time.Duration // 当前间隔
	maxInterval     time.Duration // 最大间隔
	minInterval     time.Duration // 最小间隔
	successCount    int           // 连续成功次数
	failCount       int           // 连续失败次数
	lastRequest     time.Time     // 上次请求时间
}

// NewAdaptiveRateLimiter 创建自适应限速器
func NewAdaptiveRateLimiter(baseInterval time.Duration) *AdaptiveRateLimiter {
	if baseInterval <= 0 {
		baseInterval = 500 * time.Millisecond
	}

	return &AdaptiveRateLimiter{
		baseInterval:    baseInterval,
		currentInterval: baseInterval,
		maxInterval:     baseInterval * 10,              // 最大 10 倍基准
		minInterval:     baseInterval / 5,              // 最小 0.2 倍基准
		lastRequest:     time.Now().Add(-baseInterval), // 允许立即开始
	}
}

// NewAdaptiveRateLimiterWithLimits 创建带自定义限制的限速器
func NewAdaptiveRateLimiterWithLimits(baseInterval, minInterval, maxInterval time.Duration) *AdaptiveRateLimiter {
	if baseInterval <= 0 {
		baseInterval = 500 * time.Millisecond
	}
	if minInterval <= 0 {
		minInterval = 100 * time.Millisecond
	}
	if maxInterval <= 0 {
		maxInterval = 5 * time.Second
	}

	return &AdaptiveRateLimiter{
		baseInterval:    baseInterval,
		currentInterval: baseInterval,
		maxInterval:     maxInterval,
		minInterval:     minInterval,
		lastRequest:     time.Now().Add(-baseInterval),
	}
}

// Wait 等待直到可以发送下一个请求
func (r *AdaptiveRateLimiter) Wait() {
	r.mu.Lock()
	interval := r.currentInterval
	elapsed := time.Since(r.lastRequest)
	r.mu.Unlock()

	if elapsed < interval {
		time.Sleep(interval - elapsed)
	}

	r.mu.Lock()
	r.lastRequest = time.Now()
	r.mu.Unlock()
}

// TryAcquire 尝试获取许可，如果需要等待则返回等待时间
func (r *AdaptiveRateLimiter) TryAcquire() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	elapsed := time.Since(r.lastRequest)
	if elapsed >= r.currentInterval {
		r.lastRequest = time.Now()
		return 0
	}

	return r.currentInterval - elapsed
}

// RecordSuccess 记录成功请求
func (r *AdaptiveRateLimiter) RecordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.successCount++
	r.failCount = 0

	// 连续成功 10 次后加速
	if r.successCount >= 10 {
		r.currentInterval = time.Duration(float64(r.currentInterval) * 0.9)
		if r.currentInterval < r.minInterval {
			r.currentInterval = r.minInterval
		}
		r.successCount = 0
	}
}

// RecordFailure 记录失败请求
func (r *AdaptiveRateLimiter) RecordFailure(statusCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failCount++
	r.successCount = 0

	// 根据状态码调整速率
	switch {
	case statusCode == 429: // Too Many Requests
		// 大幅降速
		r.currentInterval = r.currentInterval * 3
	case statusCode == 503: // Service Unavailable
		// 显著降速
		r.currentInterval = r.currentInterval * 2
	case statusCode >= 500:
		// 服务器错误，适度降速
		r.currentInterval = time.Duration(float64(r.currentInterval) * 1.5)
	case statusCode >= 400:
		// 客户端错误，轻微降速
		r.currentInterval = time.Duration(float64(r.currentInterval) * 1.2)
	default:
		// 其他失败，适度降速
		r.currentInterval = time.Duration(float64(r.currentInterval) * 1.3)
	}

	if r.currentInterval > r.maxInterval {
		r.currentInterval = r.maxInterval
	}
}

// RecordTimeout 记录超时
func (r *AdaptiveRateLimiter) RecordTimeout() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failCount++
	r.successCount = 0

	// 超时通常意味着服务器压力大，适度降速
	r.currentInterval = time.Duration(float64(r.currentInterval) * 1.5)
	if r.currentInterval > r.maxInterval {
		r.currentInterval = r.maxInterval
	}
}

// Reset 重置为基准速率
func (r *AdaptiveRateLimiter) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.currentInterval = r.baseInterval
	r.successCount = 0
	r.failCount = 0
}

// GetCurrentInterval 获取当前间隔
func (r *AdaptiveRateLimiter) GetCurrentInterval() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.currentInterval
}

// Stats 返回限速器统计信息
type RateLimitStats struct {
	BaseInterval    time.Duration `json:"base_interval_ms"`
	CurrentInterval time.Duration `json:"current_interval_ms"`
	MinInterval     time.Duration `json:"min_interval_ms"`
	MaxInterval     time.Duration `json:"max_interval_ms"`
	SuccessCount    int           `json:"success_count"`
	FailCount       int           `json:"fail_count"`
}

func (r *AdaptiveRateLimiter) Stats() RateLimitStats {
	r.mu.Lock()
	defer r.mu.Unlock()

	return RateLimitStats{
		BaseInterval:    r.baseInterval / time.Millisecond,
		CurrentInterval: r.currentInterval / time.Millisecond,
		MinInterval:     r.minInterval / time.Millisecond,
		MaxInterval:     r.maxInterval / time.Millisecond,
		SuccessCount:    r.successCount,
		FailCount:       r.failCount,
	}
}

// PerDomainRateLimiter 每域名独立限速器
type PerDomainRateLimiter struct {
	mu           sync.RWMutex
	limiters     map[string]*AdaptiveRateLimiter
	baseInterval time.Duration
	minInterval  time.Duration
	maxInterval  time.Duration
}

// NewPerDomainRateLimiter 创建每域名限速器
func NewPerDomainRateLimiter(baseInterval, minInterval, maxInterval time.Duration) *PerDomainRateLimiter {
	return &PerDomainRateLimiter{
		limiters:     make(map[string]*AdaptiveRateLimiter),
		baseInterval: baseInterval,
		minInterval:  minInterval,
		maxInterval:  maxInterval,
	}
}

// GetLimiter 获取域名对应的限速器
func (p *PerDomainRateLimiter) GetLimiter(domain string) *AdaptiveRateLimiter {
	p.mu.RLock()
	limiter, ok := p.limiters[domain]
	p.mu.RUnlock()

	if ok {
		return limiter
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 双重检查
	if limiter, ok := p.limiters[domain]; ok {
		return limiter
	}

	limiter = NewAdaptiveRateLimiterWithLimits(p.baseInterval, p.minInterval, p.maxInterval)
	p.limiters[domain] = limiter
	return limiter
}

// Wait 等待指定域名的请求许可
func (p *PerDomainRateLimiter) Wait(domain string) {
	p.GetLimiter(domain).Wait()
}

// RecordSuccess 记录域名请求成功
func (p *PerDomainRateLimiter) RecordSuccess(domain string) {
	p.GetLimiter(domain).RecordSuccess()
}

// RecordFailure 记录域名请求失败
func (p *PerDomainRateLimiter) RecordFailure(domain string, statusCode int) {
	p.GetLimiter(domain).RecordFailure(statusCode)
}
