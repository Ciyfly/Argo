package engine

import (
	"sync"
	"time"
)

// ResponseCache 响应缓存
type ResponseCache struct {
	mu       sync.RWMutex
	cache    map[string]*CacheEntry
	maxSize  int
	ttl      time.Duration
	stopCh   chan struct{}
}

// CacheEntry 缓存条目
type CacheEntry struct {
	URL         string
	StatusCode  int
	ContentType string
	Body        []byte
	Headers     map[string]string
	CachedAt    time.Time
	HitCount    int64
}

// ResponseCacheConfig 缓存配置
type ResponseCacheConfig struct {
	MaxSize int           // 最大缓存条目数
	TTL     time.Duration // 缓存过期时间
}

// DefaultResponseCacheConfig 默认配置
func DefaultResponseCacheConfig() ResponseCacheConfig {
	return ResponseCacheConfig{
		MaxSize: 1000,
		TTL:     10 * time.Minute,
	}
}

// NewResponseCache 创建响应缓存
func NewResponseCache(cfg ResponseCacheConfig) *ResponseCache {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 1000
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 10 * time.Minute
	}

	rc := &ResponseCache{
		cache:   make(map[string]*CacheEntry),
		maxSize: cfg.MaxSize,
		ttl:     cfg.TTL,
		stopCh:  make(chan struct{}),
	}

	// 启动清理协程
	go rc.cleanupLoop()

	return rc
}

// Get 获取缓存
func (rc *ResponseCache) Get(url string) (*CacheEntry, bool) {
	rc.mu.RLock()
	entry, ok := rc.cache[url]
	rc.mu.RUnlock()

	if !ok {
		return nil, false
	}

	// 检查是否过期
	if time.Since(entry.CachedAt) > rc.ttl {
		rc.mu.Lock()
		delete(rc.cache, url)
		rc.mu.Unlock()
		return nil, false
	}

	entry.HitCount++
	return entry, true
}

// Set 设置缓存
func (rc *ResponseCache) Set(url string, entry *CacheEntry) {
	if entry == nil {
		return
	}

	rc.mu.Lock()
	defer rc.mu.Unlock()

	// 检查容量
	if len(rc.cache) >= rc.maxSize {
		rc.evict()
	}

	entry.CachedAt = time.Now()
	rc.cache[url] = entry
}

// evict 淘汰旧条目（需要持有锁）
func (rc *ResponseCache) evict() {
	// LRU策略：删除最旧的条目
	var oldestURL string
	var oldestTime time.Time

	for url, entry := range rc.cache {
		if oldestURL == "" || entry.CachedAt.Before(oldestTime) {
			oldestURL = url
			oldestTime = entry.CachedAt
		}
	}

	if oldestURL != "" {
		delete(rc.cache, oldestURL)
	}
}

// cleanupLoop 定期清理过期条目
func (rc *ResponseCache) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-rc.stopCh:
			return
		case <-ticker.C:
			rc.cleanup()
		}
	}
}

// cleanup 清理过期条目
func (rc *ResponseCache) cleanup() {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	now := time.Now()
	for url, entry := range rc.cache {
		if now.Sub(entry.CachedAt) > rc.ttl {
			delete(rc.cache, url)
		}
	}
}

// Close 关闭缓存
func (rc *ResponseCache) Close() {
	close(rc.stopCh)
}

// Stats 获取统计信息
func (rc *ResponseCache) Stats() CacheStats {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	var totalHits int64
	for _, entry := range rc.cache {
		totalHits += entry.HitCount
	}

	return CacheStats{
		Size:      len(rc.cache),
		MaxSize:   rc.maxSize,
		TotalHits: totalHits,
	}
}

// CacheStats 缓存统计
type CacheStats struct {
	Size      int   `json:"size"`
	MaxSize   int   `json:"max_size"`
	TotalHits int64 `json:"total_hits"`
}

// ResourceFilter 资源过滤器（优化请求拦截）
type ResourceFilter struct {
	// 需要拦截的资源类型
	blockedTypes map[string]bool
	// 需要拦截的扩展名
	blockedExts  map[string]bool
	// 白名单域名（不拦截）
	whitelistDomains map[string]bool
	mu sync.RWMutex
}

// NewResourceFilter 创建资源过滤器
func NewResourceFilter() *ResourceFilter {
	rf := &ResourceFilter{
		blockedTypes: map[string]bool{
			"Image":     true,
			"Font":      true,
			"Media":     true,
			"TextTrack": true,
		},
		blockedExts: map[string]bool{
			// 图片
			".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
			".webp": true, ".svg": true, ".ico": true, ".bmp": true,
			".tiff": true, ".tif": true, ".avif": true, ".heic": true,
			// 字体
			".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
			// 媒体
			".mp4": true, ".webm": true, ".ogg": true, ".mp3": true,
			".wav": true, ".flac": true, ".aac": true, ".m4a": true,
			".avi": true, ".mov": true, ".wmv": true, ".mkv": true,
			".m3u8": true, ".ts": true,
			// 压缩包
			".zip": true, ".rar": true, ".7z": true, ".tar": true,
			".gz": true, ".bz2": true, ".xz": true,
			// 二进制
			".exe": true, ".dmg": true, ".pkg": true, ".deb": true,
			".rpm": true, ".apk": true, ".ipa": true, ".msi": true,
			// 文档（可选）
			".pdf": true,
			// Source maps
			".map": true,
		},
		whitelistDomains: make(map[string]bool),
	}
	return rf
}

// ShouldBlock 检查是否应该拦截资源
func (rf *ResourceFilter) ShouldBlock(resourceType, urlStr string) bool {
	rf.mu.RLock()
	defer rf.mu.RUnlock()

	// 检查资源类型
	if rf.blockedTypes[resourceType] {
		return true
	}

	// 检查扩展名
	ext := getExtension(urlStr)
	if rf.blockedExts[ext] {
		return true
	}

	return false
}

// AddBlockedType 添加需要拦截的资源类型
func (rf *ResourceFilter) AddBlockedType(resourceType string) {
	rf.mu.Lock()
	rf.blockedTypes[resourceType] = true
	rf.mu.Unlock()
}

// AddBlockedExt 添加需要拦截的扩展名
func (rf *ResourceFilter) AddBlockedExt(ext string) {
	rf.mu.Lock()
	rf.blockedExts[ext] = true
	rf.mu.Unlock()
}

// AddWhitelistDomain 添加白名单域名
func (rf *ResourceFilter) AddWhitelistDomain(domain string) {
	rf.mu.Lock()
	rf.whitelistDomains[domain] = true
	rf.mu.Unlock()
}

// getExtension 获取URL的扩展名
func getExtension(urlStr string) string {
	// 移除查询参数
	if idx := indexOf(urlStr, "?"); idx > 0 {
		urlStr = urlStr[:idx]
	}
	// 移除片段
	if idx := indexOf(urlStr, "#"); idx > 0 {
		urlStr = urlStr[:idx]
	}
	// 获取最后一个点后的内容
	lastDot := lastIndexOf(urlStr, ".")
	lastSlash := lastIndexOf(urlStr, "/")
	if lastDot > lastSlash && lastDot < len(urlStr)-1 {
		ext := urlStr[lastDot:]
		if len(ext) <= 6 {
			return toLower(ext)
		}
	}
	return ""
}

// 简单的字符串辅助函数
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func lastIndexOf(s, substr string) int {
	for i := len(s) - len(substr); i >= 0; i-- {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
