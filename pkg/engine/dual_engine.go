package engine

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/ratelimit"
	"argo/pkg/static"
	"compress/gzip"
	"crypto/tls"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EngineType 引擎类型
type EngineType int

const (
	// EngineTypeAuto 自动选择引擎
	EngineTypeAuto EngineType = iota
	// EngineTypeStandard 标准引擎 (HTTP Client) - 用于静态内容
	EngineTypeStandard
	// EngineTypeHybrid 混合引擎 (Headless Browser) - 用于动态内容
	EngineTypeHybrid
)

func (e EngineType) String() string {
	switch e {
	case EngineTypeStandard:
		return "standard"
	case EngineTypeHybrid:
		return "hybrid"
	default:
		return "auto"
	}
}

// DualEngine 双引擎架构
// 根据 URL 类型自动选择最合适的引擎：
// - Standard Engine: 使用 HTTP Client，适合 API、静态 HTML、JSON 等
// - Hybrid Engine: 使用 Headless Browser，适合 SPA、动态渲染页面
type DualEngine struct {
	mu sync.RWMutex

	// 组件
	classifier     *URLClassifier
	standardEngine *StandardEngine
	engine         *EngineInfo // Hybrid Engine (原有浏览器引擎)

	// 配置
	config *DualEngineConfig

	// 统计
	stats DualEngineStats
}

// DualEngineConfig 双引擎配置
type DualEngineConfig struct {
	// 是否启用双引擎模式
	Enabled bool

	// Standard Engine 配置
	StandardEngineWorkers   int           // 标准引擎并发数
	StandardEngineTimeout   time.Duration // 请求超时时间
	StandardEngineRetries   int           // 重试次数
	StandardEngineUserAgent string        // User-Agent

	// 分类器配置
	ClassifierConfig *URLClassifierConfig

	// 强制使用 Hybrid Engine 的模式
	ForceHybridPatterns []string

	// 强制使用 Standard Engine 的模式
	ForceStandardPatterns []string
}

// DualEngineStats 双引擎统计
type DualEngineStats struct {
	StandardRequests int64 // 标准引擎处理数
	HybridRequests   int64 // 混合引擎处理数
	StandardSuccess  int64 // 标准引擎成功数
	StandardFailed   int64 // 标准引擎失败数
	UpgradedToHybrid int64 // 升级到混合引擎数
}

// DefaultDualEngineConfig 默认双引擎配置
func DefaultDualEngineConfig() *DualEngineConfig {
	return &DualEngineConfig{
		Enabled:                 true,
		StandardEngineWorkers:   20,
		StandardEngineTimeout:   30 * time.Second,
		StandardEngineRetries:   2,
		StandardEngineUserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		ClassifierConfig:        DefaultURLClassifierConfig(),
		ForceHybridPatterns: []string{
			`/login`, `/signin`, `/auth`,
			`/checkout`, `/payment`,
			`/dashboard`, `/admin`,
		},
		ForceStandardPatterns: []string{
			`\.json$`,
			`\.xml$`,
			`\.txt$`,
			`\.css$`,
			`/api/v[0-9]+/`,
			`/graphql`,
			`/rest/`,
			`\.woff2?$`,
			`\.ttf$`,
		},
	}
}

// NewDualEngine 创建双引擎
func NewDualEngine(engine *EngineInfo, config *DualEngineConfig) *DualEngine {
	if config == nil {
		config = DefaultDualEngineConfig()
	}

	de := &DualEngine{
		engine:     engine,
		config:     config,
		classifier: NewURLClassifier(config.ClassifierConfig),
	}

	// 初始化标准引擎
	proxy := ""
	if conf.GlobalConfig != nil {
		proxy = conf.GlobalConfig.BrowserConf.Proxy
	}
	standardConfig := &StandardEngineConfig{
		Workers:    config.StandardEngineWorkers,
		Timeout:    config.StandardEngineTimeout,
		Retries:    config.StandardEngineRetries,
		UserAgent:  config.StandardEngineUserAgent,
		Proxy:      proxy,
		DualEngine: de,
	}
	de.standardEngine = NewStandardEngine(engine, standardConfig)

	return de
}

// Start 启动双引擎
func (de *DualEngine) Start() {
	if de.standardEngine != nil {
		de.standardEngine.Start()
	}
	log.Logger.Info("DualEngine: started with standard and hybrid engines")
}

// Stop 停止双引擎
func (de *DualEngine) Stop() {
	if de.standardEngine != nil {
		de.standardEngine.Stop()
	}
	log.Logger.Info("DualEngine: stopped")
}

// ProcessURL 处理 URL，自动选择合适的引擎
func (de *DualEngine) ProcessURL(uif *UrlInfo) EngineType {
	if !de.config.Enabled {
		return EngineTypeHybrid
	}

	// 1. 检查强制模式
	engineType := de.checkForcePatterns(uif.Url)
	if engineType != EngineTypeAuto {
		// 计数
		if engineType == EngineTypeStandard {
			atomic.AddInt64(&de.stats.StandardRequests, 1)
		} else {
			atomic.AddInt64(&de.stats.HybridRequests, 1)
		}
		return engineType
	}

	// 2. 使用分类器判断
	classification := de.classifier.Classify(uif.Url)

	switch classification.PageType {
	case PageTypeAPI, PageTypeStatic, PageTypeResource:
		// 静态内容使用标准引擎
		atomic.AddInt64(&de.stats.StandardRequests, 1)
		return EngineTypeStandard
	case PageTypeSPA, PageTypeDynamic:
		// 动态内容使用混合引擎
		atomic.AddInt64(&de.stats.HybridRequests, 1)
		return EngineTypeHybrid
	default:
		// 未知类型，根据置信度判断
		if classification.Confidence > 0.7 && classification.PageType == PageTypeStatic {
			atomic.AddInt64(&de.stats.StandardRequests, 1)
			return EngineTypeStandard
		}
		atomic.AddInt64(&de.stats.HybridRequests, 1)
		return EngineTypeHybrid
	}
}

// SubmitToStandard 提交到标准引擎处理
func (de *DualEngine) SubmitToStandard(uif *UrlInfo) {
	if de.standardEngine != nil {
		de.standardEngine.Submit(uif)
	}
}

// UpgradeToHybrid 将 URL 升级到混合引擎处理
// 当标准引擎发现页面需要 JS 渲染时调用
func (de *DualEngine) UpgradeToHybrid(uif *UrlInfo, reason string) {
	atomic.AddInt64(&de.stats.UpgradedToHybrid, 1)
	log.Logger.Debugf("DualEngine: upgrading %s to hybrid engine, reason: %s", uif.Url, reason)

	// 记录升级信息，供分类器学习
	de.classifier.RecordUpgrade(uif.Url, reason)

	// 提交到调度器，使用浏览器处理
	if de.engine.Scheduler != nil {
		uif.SourceType = "upgraded_from_standard"
		de.engine.Scheduler.Submit(uif)
	}
}

// checkForcePatterns 检查强制模式
func (de *DualEngine) checkForcePatterns(urlStr string) EngineType {
	// 检查强制 Hybrid 模式
	for _, pattern := range de.config.ForceHybridPatterns {
		if matched, _ := regexp.MatchString(pattern, urlStr); matched {
			return EngineTypeHybrid
		}
	}

	// 检查强制 Standard 模式
	for _, pattern := range de.config.ForceStandardPatterns {
		if matched, _ := regexp.MatchString(pattern, urlStr); matched {
			return EngineTypeStandard
		}
	}

	return EngineTypeAuto
}

// GetStats 获取统计信息
func (de *DualEngine) GetStats() DualEngineStats {
	return DualEngineStats{
		StandardRequests: atomic.LoadInt64(&de.stats.StandardRequests),
		HybridRequests:   atomic.LoadInt64(&de.stats.HybridRequests),
		StandardSuccess:  atomic.LoadInt64(&de.stats.StandardSuccess),
		StandardFailed:   atomic.LoadInt64(&de.stats.StandardFailed),
		UpgradedToHybrid: atomic.LoadInt64(&de.stats.UpgradedToHybrid),
	}
}

func (de *DualEngine) StandardSnapshot() StandardEngineSnapshot {
	if de == nil || de.standardEngine == nil {
		return StandardEngineSnapshot{}
	}
	return de.standardEngine.Snapshot()
}

func (de *DualEngine) IsStandardIdle() bool {
	if de == nil || de.standardEngine == nil {
		return true
	}
	return de.standardEngine.IsIdle()
}

// RecordStandardSuccess 记录标准引擎成功
func (de *DualEngine) RecordStandardSuccess() {
	atomic.AddInt64(&de.stats.StandardSuccess, 1)
}

// RecordStandardFailed 记录标准引擎失败
func (de *DualEngine) RecordStandardFailed() {
	atomic.AddInt64(&de.stats.StandardFailed, 1)
}

// ============================================================================
// URL Classifier - URL 分类器
// ============================================================================

// PageType 页面类型
type PageType int

const (
	PageTypeUnknown  PageType = iota
	PageTypeStatic            // 静态 HTML
	PageTypeAPI               // API 端点
	PageTypeResource          // 静态资源
	PageTypeSPA               // 单页应用
	PageTypeDynamic           // 动态渲染
)

func (p PageType) String() string {
	switch p {
	case PageTypeStatic:
		return "static"
	case PageTypeAPI:
		return "api"
	case PageTypeResource:
		return "resource"
	case PageTypeSPA:
		return "spa"
	case PageTypeDynamic:
		return "dynamic"
	default:
		return "unknown"
	}
}

// URLClassification URL 分类结果
type URLClassification struct {
	PageType   PageType
	Confidence float64 // 置信度 0-1
	Reason     string  // 分类原因
}

// URLClassifier URL 分类器
type URLClassifier struct {
	mu     sync.RWMutex
	config *URLClassifierConfig

	// 已知的动态域名/路径 (从升级记录中学习)
	knownDynamicPaths map[string]int

	// 预编译的正则
	apiPatterns      []*regexp.Regexp
	staticPatterns   []*regexp.Regexp
	resourcePatterns []*regexp.Regexp
	spaPatterns      []*regexp.Regexp
}

// URLClassifierConfig 分类器配置
type URLClassifierConfig struct {
	// API 模式
	APIPatterns []string
	// 静态页面模式
	StaticPatterns []string
	// 资源模式
	ResourcePatterns []string
	// SPA/动态模式
	SPAPatterns []string
}

// DefaultURLClassifierConfig 默认分类器配置
func DefaultURLClassifierConfig() *URLClassifierConfig {
	return &URLClassifierConfig{
		APIPatterns: []string{
			`/api/`,
			`/v[0-9]+/`,
			`/rest/`,
			`/graphql`,
			`/rpc/`,
			`/ajax/`,
			`\.json(\?|$)`,
			`\.xml(\?|$)`,
			`/ws/`,
			`/socket`,
		},
		StaticPatterns: []string{
			`\.html?(\?|$)`,
			`\.php(\?|$)`,
			`\.asp(\?|$)`,
			`\.jsp(\?|$)`,
			`/static/`,
			`/assets/`,
			`/public/`,
		},
		ResourcePatterns: []string{
			`\.css(\?|$)`,
			`\.js(\?|$)`,
			`\.map(\?|$)`,
			`\.woff2?(\?|$)`,
			`\.ttf(\?|$)`,
			`\.eot(\?|$)`,
			`\.svg(\?|$)`,
			`\.png(\?|$)`,
			`\.jpe?g(\?|$)`,
			`\.gif(\?|$)`,
			`\.ico(\?|$)`,
			`\.webp(\?|$)`,
			`\.mp[34](\?|$)`,
			`\.webm(\?|$)`,
			`\.pdf(\?|$)`,
		},
		SPAPatterns: []string{
			`#/`,                    // Hash routing
			`/_next/`,               // Next.js
			`/_nuxt/`,               // Nuxt.js
			`/static/js/main\.`,     // React build
			`/static/js/[0-9]+\.`,   // React chunks
			`/assets/index-`,        // Vite build
			`/__webpack_hmr`,        // Webpack HMR
			`/sockjs-node`,          // Dev server
			`\.module\.[a-f0-9]+\.`, // CSS modules
		},
	}
}

// NewURLClassifier 创建 URL 分类器
func NewURLClassifier(config *URLClassifierConfig) *URLClassifier {
	if config == nil {
		config = DefaultURLClassifierConfig()
	}

	c := &URLClassifier{
		config:            config,
		knownDynamicPaths: make(map[string]int),
	}

	// 预编译正则
	c.apiPatterns = compilePatterns(config.APIPatterns)
	c.staticPatterns = compilePatterns(config.StaticPatterns)
	c.resourcePatterns = compilePatterns(config.ResourcePatterns)
	c.spaPatterns = compilePatterns(config.SPAPatterns)

	return c
}

func compilePatterns(patterns []string) []*regexp.Regexp {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if re, err := regexp.Compile(`(?i)` + p); err == nil {
			result = append(result, re)
		}
	}
	return result
}

// Classify 分类 URL
func (c *URLClassifier) Classify(urlStr string) URLClassification {
	// 1. 检查已知动态路径
	c.mu.RLock()
	pathKey := c.getPathKey(urlStr)
	if count, ok := c.knownDynamicPaths[pathKey]; ok && count >= 2 {
		c.mu.RUnlock()
		return URLClassification{
			PageType:   PageTypeDynamic,
			Confidence: 0.9,
			Reason:     "known_dynamic_path",
		}
	}
	c.mu.RUnlock()

	// 2. 检查 API 模式
	for _, re := range c.apiPatterns {
		if re.MatchString(urlStr) {
			return URLClassification{
				PageType:   PageTypeAPI,
				Confidence: 0.95,
				Reason:     "api_pattern",
			}
		}
	}

	// 3. 检查资源模式
	for _, re := range c.resourcePatterns {
		if re.MatchString(urlStr) {
			return URLClassification{
				PageType:   PageTypeResource,
				Confidence: 0.99,
				Reason:     "resource_pattern",
			}
		}
	}

	// 4. 检查 SPA 模式
	for _, re := range c.spaPatterns {
		if re.MatchString(urlStr) {
			return URLClassification{
				PageType:   PageTypeSPA,
				Confidence: 0.85,
				Reason:     "spa_pattern",
			}
		}
	}

	// 5. 检查静态模式
	for _, re := range c.staticPatterns {
		if re.MatchString(urlStr) {
			return URLClassification{
				PageType:   PageTypeStatic,
				Confidence: 0.7,
				Reason:     "static_pattern",
			}
		}
	}

	// 6. 基于 URL 结构判断
	parsed, err := url.Parse(urlStr)
	if err == nil {
		// 纯路径且没有扩展名，可能是动态路由
		path := parsed.Path
		if !strings.Contains(path, ".") && path != "/" {
			// 检查是否像是 SPA 路由
			segments := strings.Split(strings.Trim(path, "/"), "/")
			if len(segments) >= 2 {
				// 多级路径且无扩展名，倾向于动态
				return URLClassification{
					PageType:   PageTypeDynamic,
					Confidence: 0.5,
					Reason:     "deep_path_no_extension",
				}
			}
		}
	}

	// 7. 默认作为静态处理 (先尝试标准引擎)
	return URLClassification{
		PageType:   PageTypeStatic,
		Confidence: 0.4,
		Reason:     "default_static",
	}
}

// RecordUpgrade 记录升级到 Hybrid 的情况，用于学习
func (c *URLClassifier) RecordUpgrade(urlStr, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pathKey := c.getPathKey(urlStr)
	c.knownDynamicPaths[pathKey]++

	// 限制缓存大小
	if len(c.knownDynamicPaths) > 10000 {
		// 清除计数较低的条目
		for k, v := range c.knownDynamicPaths {
			if v < 3 {
				delete(c.knownDynamicPaths, k)
			}
		}
	}
}

// getPathKey 获取路径的 key (去除具体参数)
func (c *URLClassifier) getPathKey(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}

	// 将路径中的数字替换为占位符
	path := parsed.Path
	// /users/123/profile -> /users/{id}/profile
	pathKey := regexp.MustCompile(`/\d+(/|$)`).ReplaceAllString(path, "/{id}$1")
	// /api/v1/users -> /api/v1/users (保留版本号)
	return parsed.Host + pathKey
}

// ============================================================================
// Standard Engine - 标准引擎 (HTTP Client)
// ============================================================================

// StandardEngine 标准引擎，使用 HTTP Client 处理静态内容
type StandardEngine struct {
	mu sync.RWMutex

	engine     *EngineInfo
	dualEngine *DualEngine
	config     *StandardEngineConfig

	// HTTP 客户端
	client *http.Client

	// 自适应限速器
	rateLimiter *ratelimit.PerDomainRateLimiter

	// 工作队列
	workQueue chan *UrlInfo
	stopCh    chan struct{}
	wg        sync.WaitGroup

	// 统计
	inflight  int64
	processed int64
	succeeded int64
	failed    int64
}

type StandardEngineSnapshot struct {
	QueueLen int   `json:"queue_len"`
	InFlight int64 `json:"in_flight"`
}

// StandardEngineConfig 标准引擎配置
type StandardEngineConfig struct {
	Workers    int
	Timeout    time.Duration
	Retries    int
	UserAgent  string
	Proxy      string
	DualEngine *DualEngine

	// 限速配置
	RateLimitBase time.Duration // 基准间隔
	RateLimitMin  time.Duration // 最小间隔
	RateLimitMax  time.Duration // 最大间隔
}

// NewStandardEngine 创建标准引擎
func NewStandardEngine(engine *EngineInfo, config *StandardEngineConfig) *StandardEngine {
	// 创建 HTTP 客户端
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}

	// 配置代理
	if config.Proxy != "" {
		if proxyURL, err := url.Parse(config.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	// 初始化限速器配置
	rateLimitBase := config.RateLimitBase
	if rateLimitBase <= 0 {
		rateLimitBase = 100 * time.Millisecond // 默认 100ms 基准间隔
	}
	rateLimitMin := config.RateLimitMin
	if rateLimitMin <= 0 {
		rateLimitMin = 20 * time.Millisecond // 默认最小 20ms
	}
	rateLimitMax := config.RateLimitMax
	if rateLimitMax <= 0 {
		rateLimitMax = 5 * time.Second // 默认最大 5s
	}

	se := &StandardEngine{
		engine:      engine,
		dualEngine:  config.DualEngine,
		config:      config,
		client:      client,
		rateLimiter: ratelimit.NewPerDomainRateLimiter(rateLimitBase, rateLimitMin, rateLimitMax),
		workQueue:   make(chan *UrlInfo, 10000),
		stopCh:      make(chan struct{}),
	}

	return se
}

// Start 启动标准引擎
func (se *StandardEngine) Start() {
	workers := se.config.Workers
	if workers <= 0 {
		workers = 10
	}

	for i := 0; i < workers; i++ {
		se.wg.Add(1)
		go se.worker(i)
	}

	log.Logger.Infof("StandardEngine: started with %d workers", workers)
}

// Stop 停止标准引擎
func (se *StandardEngine) Stop() {
	close(se.stopCh)
	se.wg.Wait()
	log.Logger.Info("StandardEngine: stopped")
}

// Submit 提交 URL 到标准引擎
func (se *StandardEngine) Submit(uif *UrlInfo) {
	select {
	case se.workQueue <- uif:
	default:
		log.Logger.Warnf("StandardEngine: queue full, dropping URL: %s", uif.Url)
	}
}

func (se *StandardEngine) Snapshot() StandardEngineSnapshot {
	if se == nil {
		return StandardEngineSnapshot{}
	}
	return StandardEngineSnapshot{
		QueueLen: len(se.workQueue),
		InFlight: atomic.LoadInt64(&se.inflight),
	}
}

func (se *StandardEngine) IsIdle() bool {
	snap := se.Snapshot()
	return snap.QueueLen == 0 && snap.InFlight == 0
}

// worker 工作协程
func (se *StandardEngine) worker(id int) {
	defer se.wg.Done()

	for {
		select {
		case <-se.stopCh:
			return
		case uif := <-se.workQueue:
			if uif == nil {
				continue
			}
			atomic.AddInt64(&se.inflight, 1)
			se.processURL(uif)
			atomic.AddInt64(&se.inflight, -1)
		}
	}
}

// processURL 处理单个 URL
func (se *StandardEngine) processURL(uif *UrlInfo) {
	atomic.AddInt64(&se.processed, 1)

	// 解析域名用于限速
	parsedURL, err := url.Parse(uif.Url)
	if err != nil {
		se.handleFailure(uif, "parse_url_failed", err)
		return
	}
	domain := parsedURL.Host

	// 自适应限速 - 等待直到可以发送请求
	if se.rateLimiter != nil {
		se.rateLimiter.Wait(domain)
	}

	// 创建请求
	req, err := http.NewRequest("GET", uif.Url, nil)
	if err != nil {
		se.handleFailure(uif, "create_request_failed", err)
		return
	}

	// 设置请求头
	se.setHeaders(req)

	// 发送请求
	resp, err := se.client.Do(req)
	if err != nil {
		// 记录超时或网络错误
		if se.rateLimiter != nil {
			se.rateLimiter.GetLimiter(domain).RecordTimeout()
		}
		se.handleFailure(uif, "request_failed", err)
		return
	}
	defer resp.Body.Close()

	// 根据响应状态码调整限速
	if se.rateLimiter != nil {
		if resp.StatusCode >= 400 {
			se.rateLimiter.RecordFailure(domain, resp.StatusCode)
		} else {
			se.rateLimiter.RecordSuccess(domain)
		}
	}

	// 处理响应
	se.processResponse(uif, resp)
}

// setHeaders 设置请求头
func (se *StandardEngine) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", se.config.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,application/json,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Cache-Control", "no-cache")
}

// processResponse 处理响应
func (se *StandardEngine) processResponse(uif *UrlInfo, resp *http.Response) {
	// 检查状态码
	if resp.StatusCode >= 400 {
		se.handleFailure(uif, "http_error", nil)
		return
	}

	// 读取响应体
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			se.handleFailure(uif, "gzip_error", err)
			return
		}
		defer gzReader.Close()
		reader = gzReader
	}

	// 限制读取大小
	limitedReader := io.LimitReader(reader, 10*1024*1024) // 10MB
	body, err := ioutil.ReadAll(limitedReader)
	if err != nil {
		se.handleFailure(uif, "read_body_failed", err)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	bodyStr := string(body)

	// 检查是否需要升级到 Hybrid 引擎
	if se.needsHybridEngine(bodyStr, contentType) {
		if se.dualEngine != nil {
			se.dualEngine.UpgradeToHybrid(uif, "needs_js_rendering")
		}
		return
	}

	// 成功处理
	atomic.AddInt64(&se.succeeded, 1)
	if se.dualEngine != nil {
		se.dualEngine.RecordStandardSuccess()
	}

	// 提取 URL
	se.extractURLs(uif, bodyStr, contentType)

	// 提交结果
	se.submitResult(uif, resp, bodyStr)

	log.Logger.Debugf("StandardEngine: processed %s [%d]", uif.Url, resp.StatusCode)
}

// needsHybridEngine 检查是否需要 Hybrid 引擎
func (se *StandardEngine) needsHybridEngine(body, contentType string) bool {
	// 检查 Content-Type
	if strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "application/xml") ||
		strings.Contains(contentType, "text/plain") {
		return false
	}

	// 检查是否是 SPA 应用
	spaIndicators := []string{
		`<div id="root"></div>`,
		`<div id="app"></div>`,
		`<div id="__next"></div>`,
		`<div id="__nuxt"></div>`,
		`window.__INITIAL_STATE__`,
		`window.__NEXT_DATA__`,
		`window.__NUXT__`,
		`<script type="module"`,
		`ng-app=`,
		`ng-controller=`,
		`v-cloak`,
		`data-reactroot`,
	}

	bodyLower := strings.ToLower(body)
	for _, indicator := range spaIndicators {
		if strings.Contains(bodyLower, strings.ToLower(indicator)) {
			return true
		}
	}

	// 检查是否内容过少 (可能是 SPA 骨架)
	if len(strings.TrimSpace(body)) < 500 && strings.Contains(body, "<script") {
		return true
	}

	return false
}

// extractURLs 从响应中提取 URL
func (se *StandardEngine) extractURLs(uif *UrlInfo, body, contentType string) {
	var discovered []static.DiscoveredURL

	if strings.Contains(contentType, "text/html") {
		// HTML 解析（含内联脚本/文本/注释的 URL）
		discovered = static.ParseHtmlWithSource(body, uif.Url)
		// 表单提取（与浏览器静态解析保持一致）
		for _, u := range static.ExtractFormURLs(body, uif.Url) {
			if u == "" {
				continue
			}
			discovered = append(discovered, static.DiscoveredURL{URL: u, SourceType: SourceTypeHTMLForm})
		}
	} else if strings.Contains(contentType, "application/json") {
		// JSON 中提取 URL
		for _, u := range se.extractURLsFromJSON(body, uif.Url) {
			if u == "" {
				continue
			}
			discovered = append(discovered, static.DiscoveredURL{URL: u, SourceType: SourceTypeJSON})
		}
	} else if strings.Contains(contentType, "javascript") {
		// JS 中提取 URL
		for _, u := range static.ParseJSWithJSluice(body, uif.Url) {
			if u == "" {
				continue
			}
			discovered = append(discovered, static.DiscoveredURL{URL: u, SourceType: SourceTypeJSFile})
		}
	}

	// 提交发现的 URL
	for _, item := range discovered {
		u := item.URL
		if u == "" || u == uif.Url {
			continue
		}
		sourceType := item.SourceType
		if sourceType == "" {
			sourceType = SourceTypeHTMLAttr
		}
		newUif := &UrlInfo{
			Url:        u,
			SourceType: sourceType,
			SourceUrl:  uif.Url,
			Depth:      uif.Depth + 1,
		}
		se.engine.PushStaticUrl(newUif)
	}
}

// extractURLsFromJSON 从 JSON 中提取 URL
func (se *StandardEngine) extractURLsFromJSON(body, currentUrl string) []string {
	// 使用正则提取 URL
	urlPattern := regexp.MustCompile(`"((?:https?://|/)[^"]+)"`)
	matches := urlPattern.FindAllStringSubmatch(body, -1)

	var urls []string
	for _, m := range matches {
		if len(m) >= 2 {
			u := m[1]
			if resolved := static.HandlerUrl(u, currentUrl); resolved != "" {
				urls = append(urls, resolved)
			}
		}
	}
	return urls
}

// submitResult 提交结果
func (se *StandardEngine) submitResult(uif *UrlInfo, resp *http.Response, body string) {
	// 构建响应头
	respHeaders := make(http.Header)
	for k, v := range resp.Header {
		respHeaders[k] = v
	}

	// 构建请求头
	reqHeaders := make(http.Header)
	reqHeaders.Set("User-Agent", se.config.UserAgent)

	// 生成请求字符串
	reqStr := se.formatRequest(resp.Request)

	pending := &PendingUrl{
		URL:             uif.Url,
		Method:          "GET",
		Host:            resp.Request.Host,
		SourceType:      uif.SourceType,
		SourceUrl:       uif.SourceUrl,
		Headers:         reqHeaders,
		Data:            "",
		Status:          resp.StatusCode,
		ResponseHeaders: respHeaders,
		ResponseBody:    body,
		RequestStr:      reqStr,
	}

	// 发送到结果队列
	se.engine.ResultQueue <- pending
}

// formatRequest 格式化请求为字符串
func (se *StandardEngine) formatRequest(req *http.Request) string {
	if req == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(req.Method)
	sb.WriteString(" ")
	sb.WriteString(req.URL.RequestURI())
	sb.WriteString(" HTTP/1.1\r\n")
	sb.WriteString("Host: ")
	sb.WriteString(req.Host)
	sb.WriteString("\r\n")
	for k, v := range req.Header {
		for _, val := range v {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(val)
			sb.WriteString("\r\n")
		}
	}
	sb.WriteString("\r\n")
	return sb.String()
}

// handleFailure 处理失败
func (se *StandardEngine) handleFailure(uif *UrlInfo, reason string, err error) {
	atomic.AddInt64(&se.failed, 1)
	if se.dualEngine != nil {
		se.dualEngine.RecordStandardFailed()
	}

	// 可以选择升级到 Hybrid 引擎重试
	if reason == "request_failed" && uif.Retries < se.config.Retries {
		uif.Retries++
		se.Submit(uif)
		return
	}

	if err != nil {
		log.Logger.Debugf("StandardEngine: failed to process %s, reason: %s, error: %v", uif.Url, reason, err)
	} else {
		log.Logger.Debugf("StandardEngine: failed to process %s, reason: %s", uif.Url, reason)
	}
}

// GetStats 获取统计信息
func (se *StandardEngine) GetStats() (processed, succeeded, failed int64) {
	return atomic.LoadInt64(&se.processed),
		atomic.LoadInt64(&se.succeeded),
		atomic.LoadInt64(&se.failed)
}

// GetRateLimiter 获取限速器
func (se *StandardEngine) GetRateLimiter() *ratelimit.PerDomainRateLimiter {
	return se.rateLimiter
}

// ============================================================================
// Browser Rate Limiter - 浏览器请求限速器
// ============================================================================

// BrowserRateLimiter 浏览器请求限速器
// 用于控制浏览器引擎对目标站点的请求速率
type BrowserRateLimiter struct {
	mu sync.RWMutex

	// 核心限速器
	limiter *ratelimit.PerDomainRateLimiter

	// 配置
	config *BrowserRateLimitConfig

	// 统计
	totalWaits     int64 // 总等待次数
	totalWaitTime  int64 // 总等待时间(纳秒)
	throttledCount int64 // 被限速次数
}

// BrowserRateLimitConfig 浏览器限速配置
type BrowserRateLimitConfig struct {
	Enabled      bool          // 是否启用
	BaseInterval time.Duration // 基准间隔
	MinInterval  time.Duration // 最小间隔
	MaxInterval  time.Duration // 最大间隔
}

// DefaultBrowserRateLimitConfig 默认浏览器限速配置
func DefaultBrowserRateLimitConfig() *BrowserRateLimitConfig {
	return &BrowserRateLimitConfig{
		Enabled:      true,
		BaseInterval: 200 * time.Millisecond, // 浏览器默认 200ms
		MinInterval:  50 * time.Millisecond,  // 最小 50ms
		MaxInterval:  10 * time.Second,       // 最大 10s
	}
}

// NewBrowserRateLimiter 创建浏览器限速器
func NewBrowserRateLimiter(config *BrowserRateLimitConfig) *BrowserRateLimiter {
	if config == nil {
		config = DefaultBrowserRateLimitConfig()
	}

	return &BrowserRateLimiter{
		limiter: ratelimit.NewPerDomainRateLimiter(
			config.BaseInterval,
			config.MinInterval,
			config.MaxInterval,
		),
		config: config,
	}
}

// Wait 等待直到可以发送请求
func (b *BrowserRateLimiter) Wait(domain string) {
	if !b.config.Enabled {
		return
	}

	start := time.Now()
	b.limiter.Wait(domain)
	waited := time.Since(start)

	if waited > time.Millisecond {
		atomic.AddInt64(&b.totalWaits, 1)
		atomic.AddInt64(&b.totalWaitTime, int64(waited))
	}
}

// TryAcquire 尝试获取许可，返回需要等待的时间
func (b *BrowserRateLimiter) TryAcquire(domain string) time.Duration {
	if !b.config.Enabled {
		return 0
	}
	return b.limiter.GetLimiter(domain).TryAcquire()
}

// RecordSuccess 记录成功请求
func (b *BrowserRateLimiter) RecordSuccess(domain string) {
	if !b.config.Enabled {
		return
	}
	b.limiter.RecordSuccess(domain)
}

// RecordFailure 记录失败请求
func (b *BrowserRateLimiter) RecordFailure(domain string, statusCode int) {
	if !b.config.Enabled {
		return
	}
	atomic.AddInt64(&b.throttledCount, 1)
	b.limiter.RecordFailure(domain, statusCode)
}

// RecordTimeout 记录超时
func (b *BrowserRateLimiter) RecordTimeout(domain string) {
	if !b.config.Enabled {
		return
	}
	b.limiter.GetLimiter(domain).RecordTimeout()
}

// GetCurrentInterval 获取当前域名的间隔
func (b *BrowserRateLimiter) GetCurrentInterval(domain string) time.Duration {
	if !b.config.Enabled {
		return 0
	}
	return b.limiter.GetLimiter(domain).GetCurrentInterval()
}

// BrowserRateLimitStats 浏览器限速统计
type BrowserRateLimitStats struct {
	Enabled        bool  `json:"enabled"`
	TotalWaits     int64 `json:"total_waits"`
	TotalWaitMs    int64 `json:"total_wait_ms"`
	ThrottledCount int64 `json:"throttled_count"`
	AvgWaitMs      int64 `json:"avg_wait_ms"`
}

// Stats 获取统计信息
func (b *BrowserRateLimiter) Stats() BrowserRateLimitStats {
	totalWaits := atomic.LoadInt64(&b.totalWaits)
	totalWaitTime := atomic.LoadInt64(&b.totalWaitTime)
	throttledCount := atomic.LoadInt64(&b.throttledCount)

	var avgWaitMs int64
	if totalWaits > 0 {
		avgWaitMs = (totalWaitTime / int64(time.Millisecond)) / totalWaits
	}

	return BrowserRateLimitStats{
		Enabled:        b.config.Enabled,
		TotalWaits:     totalWaits,
		TotalWaitMs:    totalWaitTime / int64(time.Millisecond),
		ThrottledCount: throttledCount,
		AvgWaitMs:      avgWaitMs,
	}
}

// GetPerDomainLimiter 获取底层的 PerDomainRateLimiter
func (b *BrowserRateLimiter) GetPerDomainLimiter() *ratelimit.PerDomainRateLimiter {
	return b.limiter
}
