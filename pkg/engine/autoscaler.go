package engine

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"argo/pkg/log"
)

// Autoscaler 自适应并发控制器
type Autoscaler struct {
	mu              sync.RWMutex
	minConcurrency  int
	maxConcurrency  int
	currentConcurrency int32

	// 系统资源阈值
	cpuThreshold    float64 // CPU使用率阈值
	memThreshold    float64 // 内存使用率阈值

	// 性能指标
	successCount    int64
	failureCount    int64
	timeoutCount    int64
	totalLatency    int64 // 总延迟(ms)
	requestCount    int64

	// 缩放参数
	scaleUpThreshold   int   // 连续成功多少次后扩容
	scaleDownThreshold int   // 连续失败多少次后缩容
	consecutiveSuccess int64
	consecutiveFailure int64

	// 冷却时间
	lastScaleTime   time.Time
	cooldownPeriod  time.Duration

	// 停止信号
	stopCh          chan struct{}
	stopped         int32
}

// AutoscalerConfig 自适应控制器配置
type AutoscalerConfig struct {
	MinConcurrency     int
	MaxConcurrency     int
	CPUThreshold       float64
	MemThreshold       float64
	ScaleUpThreshold   int
	ScaleDownThreshold int
	CooldownPeriod     time.Duration
}

// DefaultAutoscalerConfig 默认配置
func DefaultAutoscalerConfig() AutoscalerConfig {
	cpuCount := runtime.NumCPU()
	return AutoscalerConfig{
		MinConcurrency:     1,
		MaxConcurrency:     cpuCount * 2,
		CPUThreshold:       0.80,  // 80% CPU使用率
		MemThreshold:       0.85,  // 85% 内存使用率
		ScaleUpThreshold:   10,    // 连续10次成功后扩容
		ScaleDownThreshold: 3,     // 连续3次失败后缩容
		CooldownPeriod:     5 * time.Second,
	}
}

// NewAutoscaler 创建自适应控制器
func NewAutoscaler(cfg AutoscalerConfig) *Autoscaler {
	if cfg.MinConcurrency <= 0 {
		cfg.MinConcurrency = 1
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = runtime.NumCPU() * 2
	}
	if cfg.MaxConcurrency < cfg.MinConcurrency {
		cfg.MaxConcurrency = cfg.MinConcurrency
	}
	if cfg.CPUThreshold <= 0 || cfg.CPUThreshold > 1 {
		cfg.CPUThreshold = 0.80
	}
	if cfg.MemThreshold <= 0 || cfg.MemThreshold > 1 {
		cfg.MemThreshold = 0.85
	}
	if cfg.ScaleUpThreshold <= 0 {
		cfg.ScaleUpThreshold = 10
	}
	if cfg.ScaleDownThreshold <= 0 {
		cfg.ScaleDownThreshold = 3
	}
	if cfg.CooldownPeriod <= 0 {
		cfg.CooldownPeriod = 5 * time.Second
	}

	// 初始并发数设为最小值和最大值的中间
	initialConcurrency := (cfg.MinConcurrency + cfg.MaxConcurrency) / 2
	if initialConcurrency < cfg.MinConcurrency {
		initialConcurrency = cfg.MinConcurrency
	}

	as := &Autoscaler{
		minConcurrency:     cfg.MinConcurrency,
		maxConcurrency:     cfg.MaxConcurrency,
		currentConcurrency: int32(initialConcurrency),
		cpuThreshold:       cfg.CPUThreshold,
		memThreshold:       cfg.MemThreshold,
		scaleUpThreshold:   cfg.ScaleUpThreshold,
		scaleDownThreshold: cfg.ScaleDownThreshold,
		cooldownPeriod:     cfg.CooldownPeriod,
		lastScaleTime:      time.Now(),
		stopCh:             make(chan struct{}),
	}

	// 启动监控协程
	go as.monitorLoop()

	log.Logger.Infof("Autoscaler initialized: min=%d, max=%d, initial=%d",
		cfg.MinConcurrency, cfg.MaxConcurrency, initialConcurrency)

	return as
}

// GetConcurrency 获取当前并发数
func (as *Autoscaler) GetConcurrency() int {
	return int(atomic.LoadInt32(&as.currentConcurrency))
}

// RecordSuccess 记录成功请求
func (as *Autoscaler) RecordSuccess(latencyMs int64) {
	atomic.AddInt64(&as.successCount, 1)
	atomic.AddInt64(&as.requestCount, 1)
	atomic.AddInt64(&as.totalLatency, latencyMs)
	atomic.AddInt64(&as.consecutiveSuccess, 1)
	atomic.StoreInt64(&as.consecutiveFailure, 0)

	// 检查是否需要扩容
	if atomic.LoadInt64(&as.consecutiveSuccess) >= int64(as.scaleUpThreshold) {
		as.tryScaleUp()
	}
}

// RecordFailure 记录失败请求
func (as *Autoscaler) RecordFailure() {
	atomic.AddInt64(&as.failureCount, 1)
	atomic.AddInt64(&as.requestCount, 1)
	atomic.AddInt64(&as.consecutiveFailure, 1)
	atomic.StoreInt64(&as.consecutiveSuccess, 0)

	// 检查是否需要缩容
	if atomic.LoadInt64(&as.consecutiveFailure) >= int64(as.scaleDownThreshold) {
		as.tryScaleDown()
	}
}

// RecordTimeout 记录超时请求
func (as *Autoscaler) RecordTimeout() {
	atomic.AddInt64(&as.timeoutCount, 1)
	atomic.AddInt64(&as.failureCount, 1)
	atomic.AddInt64(&as.requestCount, 1)
	atomic.AddInt64(&as.consecutiveFailure, 1)
	atomic.StoreInt64(&as.consecutiveSuccess, 0)

	// 超时通常意味着系统过载，更积极地缩容
	as.tryScaleDown()
}

// tryScaleUp 尝试扩容
func (as *Autoscaler) tryScaleUp() {
	as.mu.Lock()
	defer as.mu.Unlock()

	// 检查冷却时间
	if time.Since(as.lastScaleTime) < as.cooldownPeriod {
		return
	}

	current := atomic.LoadInt32(&as.currentConcurrency)
	if int(current) >= as.maxConcurrency {
		return
	}

	// 检查系统资源
	cpuUsage, memUsage := as.getSystemUsage()
	if cpuUsage > as.cpuThreshold || memUsage > as.memThreshold {
		log.Logger.Debugf("Autoscaler: skip scale up due to high resource usage (cpu=%.2f, mem=%.2f)",
			cpuUsage, memUsage)
		return
	}

	newConcurrency := current + 1
	atomic.StoreInt32(&as.currentConcurrency, newConcurrency)
	atomic.StoreInt64(&as.consecutiveSuccess, 0)
	as.lastScaleTime = time.Now()

	log.Logger.Infof("Autoscaler: scaled up %d -> %d", current, newConcurrency)
}

// tryScaleDown 尝试缩容
func (as *Autoscaler) tryScaleDown() {
	as.mu.Lock()
	defer as.mu.Unlock()

	// 检查冷却时间
	if time.Since(as.lastScaleTime) < as.cooldownPeriod {
		return
	}

	current := atomic.LoadInt32(&as.currentConcurrency)
	if int(current) <= as.minConcurrency {
		return
	}

	newConcurrency := current - 1
	atomic.StoreInt32(&as.currentConcurrency, newConcurrency)
	atomic.StoreInt64(&as.consecutiveFailure, 0)
	as.lastScaleTime = time.Now()

	log.Logger.Infof("Autoscaler: scaled down %d -> %d", current, newConcurrency)
}

// monitorLoop 监控循环
func (as *Autoscaler) monitorLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-as.stopCh:
			return
		case <-ticker.C:
			as.adjustBasedOnResources()
		}
	}
}

// adjustBasedOnResources 根据系统资源调整
func (as *Autoscaler) adjustBasedOnResources() {
	cpuUsage, memUsage := as.getSystemUsage()

	as.mu.Lock()
	defer as.mu.Unlock()

	current := atomic.LoadInt32(&as.currentConcurrency)

	// 如果资源使用率过高，主动缩容
	if cpuUsage > as.cpuThreshold || memUsage > as.memThreshold {
		if int(current) > as.minConcurrency && time.Since(as.lastScaleTime) >= as.cooldownPeriod {
			newConcurrency := current - 1
			atomic.StoreInt32(&as.currentConcurrency, newConcurrency)
			as.lastScaleTime = time.Now()
			log.Logger.Warnf("Autoscaler: resource pressure, scaled down %d -> %d (cpu=%.2f, mem=%.2f)",
				current, newConcurrency, cpuUsage, memUsage)
		}
	}
}

// getSystemUsage 获取系统资源使用率
func (as *Autoscaler) getSystemUsage() (cpuUsage, memUsage float64) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// 简化的内存使用率计算
	// 实际项目中可能需要更精确的系统级监控
	memUsage = float64(m.Alloc) / float64(m.Sys)
	if memUsage > 1 {
		memUsage = 1
	}

	// CPU使用率估算（基于goroutine数量和CPU数量）
	numGoroutine := runtime.NumGoroutine()
	numCPU := runtime.NumCPU()
	cpuUsage = float64(numGoroutine) / float64(numCPU*100)
	if cpuUsage > 1 {
		cpuUsage = 1
	}

	return cpuUsage, memUsage
}

// Stats 获取统计信息
func (as *Autoscaler) Stats() AutoscalerStats {
	reqCount := atomic.LoadInt64(&as.requestCount)
	avgLatency := int64(0)
	if reqCount > 0 {
		avgLatency = atomic.LoadInt64(&as.totalLatency) / reqCount
	}

	cpuUsage, memUsage := as.getSystemUsage()

	return AutoscalerStats{
		CurrentConcurrency: int(atomic.LoadInt32(&as.currentConcurrency)),
		MinConcurrency:     as.minConcurrency,
		MaxConcurrency:     as.maxConcurrency,
		SuccessCount:       atomic.LoadInt64(&as.successCount),
		FailureCount:       atomic.LoadInt64(&as.failureCount),
		TimeoutCount:       atomic.LoadInt64(&as.timeoutCount),
		AvgLatencyMs:       avgLatency,
		CPUUsage:           cpuUsage,
		MemUsage:           memUsage,
	}
}

// AutoscalerStats 统计信息
type AutoscalerStats struct {
	CurrentConcurrency int     `json:"current_concurrency"`
	MinConcurrency     int     `json:"min_concurrency"`
	MaxConcurrency     int     `json:"max_concurrency"`
	SuccessCount       int64   `json:"success_count"`
	FailureCount       int64   `json:"failure_count"`
	TimeoutCount       int64   `json:"timeout_count"`
	AvgLatencyMs       int64   `json:"avg_latency_ms"`
	CPUUsage           float64 `json:"cpu_usage"`
	MemUsage           float64 `json:"mem_usage"`
}

// Stop 停止自适应控制器
func (as *Autoscaler) Stop() {
	if atomic.CompareAndSwapInt32(&as.stopped, 0, 1) {
		close(as.stopCh)
	}
}

// SetConcurrency 手动设置并发数
func (as *Autoscaler) SetConcurrency(n int) {
	as.mu.Lock()
	defer as.mu.Unlock()

	if n < as.minConcurrency {
		n = as.minConcurrency
	}
	if n > as.maxConcurrency {
		n = as.maxConcurrency
	}

	old := atomic.SwapInt32(&as.currentConcurrency, int32(n))
	log.Logger.Infof("Autoscaler: manually set concurrency %d -> %d", old, n)
}
