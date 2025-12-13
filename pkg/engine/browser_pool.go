package engine

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"argo/pkg/conf"
	"argo/pkg/log"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// BrowserInstance 浏览器实例
type BrowserInstance struct {
	Browser     *rod.Browser
	Launcher    *launcher.Launcher
	ActiveTabs  int32         // 当前活跃Tab数
	MaxTabs     int           // 最大Tab数
	CreatedAt   time.Time     // 创建时间
	LastUsedAt  time.Time     // 最后使用时间
	TotalTabs   int64         // 总处理Tab数
	mu          sync.Mutex
	closed      bool
}

// BrowserPool 浏览器池
type BrowserPool struct {
	instances     []*BrowserInstance
	mu            sync.RWMutex
	maxInstances  int           // 最大浏览器实例数
	maxTabsPerBrowser int       // 每个浏览器最大Tab数
	idleTimeout   time.Duration // 空闲超时时间
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	closed        int32
}

// BrowserPoolConfig 浏览器池配置
type BrowserPoolConfig struct {
	MaxInstances      int           // 最大浏览器实例数
	MaxTabsPerBrowser int           // 每个浏览器最大Tab数
	IdleTimeout       time.Duration // 空闲超时时间
}

// DefaultBrowserPoolConfig 默认配置
func DefaultBrowserPoolConfig() BrowserPoolConfig {
	maxInstances := conf.GlobalConfig.BrowserConf.PoolMaxInstances
	if maxInstances <= 0 {
		maxInstances = conf.GlobalConfig.BrowserConf.TabCount
	}
	if maxInstances <= 0 {
		maxInstances = 5
	}
	// 根据CPU核心数调整
	cpuCount := runtime.NumCPU()
	if maxInstances > cpuCount*2 {
		maxInstances = cpuCount * 2
	}

	maxTabsPerBrowser := conf.GlobalConfig.BrowserConf.PoolMaxTabs
	if maxTabsPerBrowser <= 0 {
		maxTabsPerBrowser = 3 // 每个浏览器最多3个Tab
	}
	if conf.GlobalConfig.BrowserConf.UnHeadless {
		maxTabsPerBrowser = 1 // 非无头模式每个浏览器1个Tab
	}

	return BrowserPoolConfig{
		MaxInstances:      maxInstances,
		MaxTabsPerBrowser: maxTabsPerBrowser,
		IdleTimeout:       5 * time.Minute,
	}
}

// NewBrowserPool 创建浏览器池
func NewBrowserPool(cfg BrowserPoolConfig) *BrowserPool {
	if cfg.MaxInstances <= 0 {
		cfg.MaxInstances = 5
	}
	if cfg.MaxTabsPerBrowser <= 0 {
		cfg.MaxTabsPerBrowser = 3
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}

	ctx, cancel := context.WithCancel(context.Background())
	pool := &BrowserPool{
		instances:         make([]*BrowserInstance, 0, cfg.MaxInstances),
		maxInstances:      cfg.MaxInstances,
		maxTabsPerBrowser: cfg.MaxTabsPerBrowser,
		idleTimeout:       cfg.IdleTimeout,
		ctx:               ctx,
		cancel:            cancel,
	}

	// 启动清理协程
	pool.wg.Add(1)
	go pool.cleanupLoop()

	log.Logger.Infof("BrowserPool initialized: maxInstances=%d, maxTabsPerBrowser=%d",
		cfg.MaxInstances, cfg.MaxTabsPerBrowser)

	return pool
}

// Acquire 获取一个可用的浏览器实例
func (p *BrowserPool) Acquire() (*BrowserInstance, error) {
	if atomic.LoadInt32(&p.closed) == 1 {
		return nil, context.Canceled
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 1. 查找有空闲槽位的现有实例
	for _, inst := range p.instances {
		if inst.closed {
			continue
		}
		if atomic.LoadInt32(&inst.ActiveTabs) < int32(inst.MaxTabs) {
			atomic.AddInt32(&inst.ActiveTabs, 1)
			inst.LastUsedAt = time.Now()
			atomic.AddInt64(&inst.TotalTabs, 1)
			return inst, nil
		}
	}

	// 2. 如果没有可用实例且未达上限，创建新实例
	if len(p.instances) < p.maxInstances {
		inst, err := p.createInstance()
		if err != nil {
			return nil, err
		}
		atomic.AddInt32(&inst.ActiveTabs, 1)
		atomic.AddInt64(&inst.TotalTabs, 1)
		p.instances = append(p.instances, inst)
		log.Logger.Debugf("BrowserPool: created new instance, total=%d", len(p.instances))
		return inst, nil
	}

	// 3. 所有实例都满了，等待或返回最少使用的
	var leastUsed *BrowserInstance
	var minTabs int32 = int32(p.maxTabsPerBrowser + 1)
	for _, inst := range p.instances {
		if inst.closed {
			continue
		}
		tabs := atomic.LoadInt32(&inst.ActiveTabs)
		if tabs < minTabs {
			minTabs = tabs
			leastUsed = inst
		}
	}

	if leastUsed != nil {
		atomic.AddInt32(&leastUsed.ActiveTabs, 1)
		leastUsed.LastUsedAt = time.Now()
		atomic.AddInt64(&leastUsed.TotalTabs, 1)
		return leastUsed, nil
	}

	// 4. 强制创建一个新实例
	inst, err := p.createInstance()
	if err != nil {
		return nil, err
	}
	atomic.AddInt32(&inst.ActiveTabs, 1)
	atomic.AddInt64(&inst.TotalTabs, 1)
	p.instances = append(p.instances, inst)
	return inst, nil
}

// Release 释放浏览器实例的一个Tab槽位
func (p *BrowserPool) Release(inst *BrowserInstance) {
	if inst == nil {
		return
	}
	atomic.AddInt32(&inst.ActiveTabs, -1)
	inst.LastUsedAt = time.Now()
}

// createInstance 创建新的浏览器实例
func (p *BrowserPool) createInstance() (*BrowserInstance, error) {
	options := NewBrowserOptions()
	browser := rod.New()

	if conf.GlobalConfig.BrowserConf.Trace {
		browser = browser.Trace(true)
	}

	browser = browser.ControlURL(options.MustLaunch()).MustConnect().NoDefaultDevice().MustIncognito()
	browser.MustIgnoreCertErrors(true)

	inst := &BrowserInstance{
		Browser:    browser,
		Launcher:   options,
		MaxTabs:    p.maxTabsPerBrowser,
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	}

	return inst, nil
}

// cleanupLoop 定期清理空闲实例
func (p *BrowserPool) cleanupLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.cleanup()
		}
	}
}

// cleanup 清理空闲实例
func (p *BrowserPool) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.instances) <= 1 {
		return // 保留至少一个实例
	}

	now := time.Now()
	newInstances := make([]*BrowserInstance, 0, len(p.instances))

	for _, inst := range p.instances {
		if inst.closed {
			continue
		}

		// 如果实例空闲且超过超时时间，关闭它
		if atomic.LoadInt32(&inst.ActiveTabs) == 0 && now.Sub(inst.LastUsedAt) > p.idleTimeout {
			log.Logger.Debugf("BrowserPool: closing idle instance, age=%s", now.Sub(inst.CreatedAt))
			p.closeInstance(inst)
			continue
		}

		newInstances = append(newInstances, inst)
	}

	p.instances = newInstances
}

// closeInstance 关闭单个实例
func (p *BrowserPool) closeInstance(inst *BrowserInstance) {
	if inst == nil || inst.closed {
		return
	}
	inst.closed = true

	if inst.Browser != nil {
		inst.Browser.Close()
	}
	if inst.Launcher != nil {
		inst.Launcher.Kill()
	}
}

// Close 关闭浏览器池
func (p *BrowserPool) Close() {
	if !atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		return
	}

	p.cancel()

	p.mu.Lock()
	for _, inst := range p.instances {
		p.closeInstance(inst)
	}
	p.instances = nil
	p.mu.Unlock()

	p.wg.Wait()
	log.Logger.Info("BrowserPool closed")
}

// Stats 返回池统计信息
func (p *BrowserPool) Stats() BrowserPoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := BrowserPoolStats{
		TotalInstances: len(p.instances),
		MaxInstances:   p.maxInstances,
	}

	for _, inst := range p.instances {
		if inst.closed {
			continue
		}
		tabs := atomic.LoadInt32(&inst.ActiveTabs)
		stats.ActiveTabs += int(tabs)
		stats.TotalTabsProcessed += atomic.LoadInt64(&inst.TotalTabs)
		if tabs == 0 {
			stats.IdleInstances++
		}
	}

	return stats
}

// BrowserPoolStats 池统计信息
type BrowserPoolStats struct {
	TotalInstances     int   `json:"total_instances"`
	IdleInstances      int   `json:"idle_instances"`
	MaxInstances       int   `json:"max_instances"`
	ActiveTabs         int   `json:"active_tabs"`
	TotalTabsProcessed int64 `json:"total_tabs_processed"`
}

// GetBrowser 获取浏览器实例（兼容旧接口）
func (inst *BrowserInstance) GetBrowser() *rod.Browser {
	return inst.Browser
}

// GetLauncher 获取启动器实例（兼容旧接口）
func (inst *BrowserInstance) GetLauncher() *launcher.Launcher {
	return inst.Launcher
}
