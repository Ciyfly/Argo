package engine

import (
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"argo/pkg/log"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// PassiveCrawler 被动爬取模式
// 通过监听网络请求和页面事件来收集URL，减少主动请求
type PassiveCrawler struct {
	mu             sync.RWMutex
	engine         *EngineInfo
	discoveredURLs map[string]bool
	pendingURLs    chan *DiscoveredURL
	eventListeners []func(event *CrawlEvent)

	// 配置
	config *PassiveCrawlerConfig

	// 统计
	urlsDiscovered  int64
	requestsCaught  int64
	eventsProcessed int64

	// 控制
	running int32
	stopCh  chan struct{}
}

// PassiveCrawlerConfig 被动爬取配置
type PassiveCrawlerConfig struct {
	EnableNetworkMonitor bool          // 启用网络监控
	EnableDOMObserver    bool          // 启用DOM变化观察
	EnableEventCapture   bool          // 启用事件捕获
	MaxPendingURLs       int           // 最大待处理URL数
	ProcessInterval      time.Duration // 处理间隔
	URLPatterns          []string      // URL匹配模式
	ExcludePatterns      []string      // 排除模式
}

// DiscoveredURL 发现的URL
type DiscoveredURL struct {
	URL        string
	Source     string // 来源: network, dom, event, script
	Method     string // HTTP方法
	Headers    map[string]string
	RefererURL string
	Timestamp  time.Time
	Priority   int
}

// CrawlEvent 爬取事件
type CrawlEvent struct {
	Type      string // url_discovered, request_caught, dom_changed
	URL       string
	Data      interface{}
	Timestamp time.Time
}

// DefaultPassiveCrawlerConfig 默认配置
func DefaultPassiveCrawlerConfig() *PassiveCrawlerConfig {
	return &PassiveCrawlerConfig{
		EnableNetworkMonitor: true,
		EnableDOMObserver:    true,
		EnableEventCapture:   true,
		MaxPendingURLs:       10000,
		ProcessInterval:      100 * time.Millisecond,
		URLPatterns:          []string{},
		ExcludePatterns: []string{
			`\.(jpg|jpeg|png|gif|webp|svg|ico|bmp)(\?|$)`,
			`\.(css|woff|woff2|ttf|otf|eot)(\?|$)`,
			`\.(mp4|webm|mp3|wav|ogg|flac)(\?|$)`,
			`\.(pdf|doc|docx|xls|xlsx|ppt)(\?|$)`,
			`google-analytics\.com`,
			`googletagmanager\.com`,
			`facebook\.com/tr`,
			`doubleclick\.net`,
		},
	}
}

// NewPassiveCrawler 创建被动爬取器
func NewPassiveCrawler(engine *EngineInfo, config *PassiveCrawlerConfig) *PassiveCrawler {
	if config == nil {
		config = DefaultPassiveCrawlerConfig()
	}

	pc := &PassiveCrawler{
		engine:         engine,
		discoveredURLs: make(map[string]bool),
		pendingURLs:    make(chan *DiscoveredURL, config.MaxPendingURLs),
		config:         config,
		stopCh:         make(chan struct{}),
	}

	return pc
}

// Start 启动被动爬取
func (pc *PassiveCrawler) Start() {
	if !atomic.CompareAndSwapInt32(&pc.running, 0, 1) {
		return
	}

	go pc.processLoop()
	log.Logger.Info("PassiveCrawler started")
}

// Stop 停止被动爬取
func (pc *PassiveCrawler) Stop() {
	if atomic.CompareAndSwapInt32(&pc.running, 1, 0) {
		close(pc.stopCh)
		log.Logger.Info("PassiveCrawler stopped")
	}
}

// processLoop 处理循环
func (pc *PassiveCrawler) processLoop() {
	ticker := time.NewTicker(pc.config.ProcessInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pc.stopCh:
			return
		case discovered := <-pc.pendingURLs:
			pc.processDiscoveredURL(discovered)
		case <-ticker.C:
			// 定期处理
		}
	}
}

// AttachToPage 将被动爬取器附加到页面
func (pc *PassiveCrawler) AttachToPage(page *rod.Page) error {
	if page == nil {
		return nil
	}

	// 启用网络监控
	if pc.config.EnableNetworkMonitor {
		go pc.monitorNetwork(page)
	}

	// 启用DOM观察
	if pc.config.EnableDOMObserver {
		go pc.observeDOM(page)
	}

	// 启用事件捕获
	if pc.config.EnableEventCapture {
		go pc.captureEvents(page)
	}

	return nil
}

// monitorNetwork 监控网络请求
func (pc *PassiveCrawler) monitorNetwork(page *rod.Page) {
	// 通过 CDP 监听网络事件
	go page.EachEvent(func(e *proto.NetworkRequestWillBeSent) {
		pc.handleNetworkRequest(e)
	})()

	go page.EachEvent(func(e *proto.NetworkResponseReceived) {
		pc.handleNetworkResponse(e)
	})()
}

// handleNetworkRequest 处理网络请求
func (pc *PassiveCrawler) handleNetworkRequest(e *proto.NetworkRequestWillBeSent) {
	if e == nil || e.Request == nil {
		return
	}

	atomic.AddInt64(&pc.requestsCaught, 1)

	urlStr := e.Request.URL
	if !pc.shouldProcess(urlStr) {
		return
	}

	// 转换 headers
	headers := make(map[string]string)
	for k, v := range e.Request.Headers {
		headers[k] = v.Str()
	}

	discovered := &DiscoveredURL{
		URL:        urlStr,
		Source:     "network",
		Method:     e.Request.Method,
		Headers:    headers,
		RefererURL: headers["Referer"],
		Timestamp:  time.Now(),
		Priority:   pc.calculatePriority(urlStr, "network"),
	}

	pc.submitURL(discovered)
}

// handleNetworkResponse 处理网络响应
func (pc *PassiveCrawler) handleNetworkResponse(e *proto.NetworkResponseReceived) {
	if e == nil || e.Response == nil {
		return
	}

	// 从响应头中提取 Location 重定向
	location := e.Response.Headers["location"]
	locationStr := location.Str()
	if locationStr != "" {
		discovered := &DiscoveredURL{
			URL:       locationStr,
			Source:    "redirect",
			Timestamp: time.Now(),
			Priority:  80,
		}
		pc.submitURL(discovered)
	}
}

// observeDOM 观察DOM变化
func (pc *PassiveCrawler) observeDOM(page *rod.Page) {
	// 注入 MutationObserver
	script := `
	() => {
		if (window.__argoObserver) return;

		const discovered = new Set();

		function extractURLs(node) {
			if (!node || !node.querySelectorAll) return;

			// 提取链接
			const links = node.querySelectorAll('a[href]');
			links.forEach(link => {
				const href = link.href;
				if (href && !discovered.has(href)) {
					discovered.add(href);
					window.__argoDiscoveredURLs = window.__argoDiscoveredURLs || [];
					window.__argoDiscoveredURLs.push({url: href, source: 'dom_link'});
				}
			});

			// 提取表单
			const forms = node.querySelectorAll('form[action]');
			forms.forEach(form => {
				const action = form.action;
				if (action && !discovered.has(action)) {
					discovered.add(action);
					window.__argoDiscoveredURLs = window.__argoDiscoveredURLs || [];
					window.__argoDiscoveredURLs.push({url: action, source: 'dom_form'});
				}
			});

			// 提取图片src (用于判断API)
			const imgs = node.querySelectorAll('img[src]');
			imgs.forEach(img => {
				const src = img.src;
				if (src && src.includes('/api/') && !discovered.has(src)) {
					discovered.add(src);
					window.__argoDiscoveredURLs = window.__argoDiscoveredURLs || [];
					window.__argoDiscoveredURLs.push({url: src, source: 'dom_api'});
				}
			});
		}

		// 初始扫描
		extractURLs(document);

		// 监听变化
		const observer = new MutationObserver((mutations) => {
			mutations.forEach(mutation => {
				mutation.addedNodes.forEach(node => {
					if (node.nodeType === 1) {
						extractURLs(node);
					}
				});
			});
		});

		observer.observe(document.body, {
			childList: true,
			subtree: true
		});

		window.__argoObserver = observer;
	}
	`

	page.Eval(script)

	// 定期收集发现的URL
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-pc.stopCh:
				return
			case <-ticker.C:
				pc.collectDOMURLs(page)
			}
		}
	}()
}

// collectDOMURLs 收集DOM中发现的URL
func (pc *PassiveCrawler) collectDOMURLs(page *rod.Page) {
	result, err := page.Eval(`
		() => {
			const urls = window.__argoDiscoveredURLs || [];
			window.__argoDiscoveredURLs = [];
			return urls;
		}
	`)

	if err != nil {
		return
	}

	arr := result.Value.Arr()
	for _, item := range arr {
		obj := item.Map()
		urlStr, _ := obj["url"].Str(), obj["source"].Str()

		if urlStr != "" && pc.shouldProcess(urlStr) {
			discovered := &DiscoveredURL{
				URL:       urlStr,
				Source:    "dom",
				Timestamp: time.Now(),
				Priority:  pc.calculatePriority(urlStr, "dom"),
			}
			pc.submitURL(discovered)
		}
	}
}

// captureEvents 捕获页面事件
func (pc *PassiveCrawler) captureEvents(page *rod.Page) {
	// 注入事件监听器
	script := `
	() => {
		if (window.__argoEventCapture) return;

		window.__argoEventURLs = [];

		// 监听点击事件
		document.addEventListener('click', function(e) {
			const target = e.target;

			// 检查是否是链接
			let link = target.closest('a');
			if (link && link.href) {
				window.__argoEventURLs.push({
					url: link.href,
					source: 'click_link',
					timestamp: Date.now()
				});
			}

			// 检查是否有 data-href 或 data-url
			const dataHref = target.dataset.href || target.dataset.url;
			if (dataHref) {
				window.__argoEventURLs.push({
					url: dataHref,
					source: 'click_data',
					timestamp: Date.now()
				});
			}
		}, true);

		// 监听 XMLHttpRequest
		const originalOpen = XMLHttpRequest.prototype.open;
		XMLHttpRequest.prototype.open = function(method, url) {
			if (url && typeof url === 'string') {
				window.__argoEventURLs.push({
					url: url,
					method: method,
					source: 'xhr',
					timestamp: Date.now()
				});
			}
			return originalOpen.apply(this, arguments);
		};

		// 监听 fetch
		const originalFetch = window.fetch;
		window.fetch = function(input, init) {
			let url = typeof input === 'string' ? input : input.url;
			if (url) {
				window.__argoEventURLs.push({
					url: url,
					method: (init && init.method) || 'GET',
					source: 'fetch',
					timestamp: Date.now()
				});
			}
			return originalFetch.apply(this, arguments);
		};

		// 监听 History API
		const originalPushState = history.pushState;
		history.pushState = function() {
			const url = arguments[2];
			if (url) {
				window.__argoEventURLs.push({
					url: url,
					source: 'pushstate',
					timestamp: Date.now()
				});
			}
			return originalPushState.apply(this, arguments);
		};

		window.__argoEventCapture = true;
	}
	`

	page.Eval(script)

	// 定期收集事件URL
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-pc.stopCh:
				return
			case <-ticker.C:
				pc.collectEventURLs(page)
			}
		}
	}()
}

// collectEventURLs 收集事件中发现的URL
func (pc *PassiveCrawler) collectEventURLs(page *rod.Page) {
	result, err := page.Eval(`
		() => {
			const urls = window.__argoEventURLs || [];
			window.__argoEventURLs = [];
			return urls;
		}
	`)

	if err != nil {
		return
	}

	arr := result.Value.Arr()
	for _, item := range arr {
		obj := item.Map()
		urlStr := obj["url"].Str()
		source := obj["source"].Str()
		method := obj["method"].Str()

		if urlStr != "" && pc.shouldProcess(urlStr) {
			// 处理相对URL
			if !strings.HasPrefix(urlStr, "http") {
				info, _ := page.Info()
				if info != nil {
					baseURL, _ := url.Parse(info.URL)
					if baseURL != nil {
						resolved, _ := baseURL.Parse(urlStr)
						if resolved != nil {
							urlStr = resolved.String()
						}
					}
				}
			}

			discovered := &DiscoveredURL{
				URL:       urlStr,
				Source:    "event_" + source,
				Method:    method,
				Timestamp: time.Now(),
				Priority:  pc.calculatePriority(urlStr, source),
			}
			pc.submitURL(discovered)
		}
	}
}

// shouldProcess 判断URL是否应该处理
func (pc *PassiveCrawler) shouldProcess(urlStr string) bool {
	if urlStr == "" {
		return false
	}

	// 跳过特殊协议
	if strings.HasPrefix(urlStr, "javascript:") ||
		strings.HasPrefix(urlStr, "data:") ||
		strings.HasPrefix(urlStr, "mailto:") ||
		strings.HasPrefix(urlStr, "tel:") {
		return false
	}

	// 检查排除模式
	urlLower := strings.ToLower(urlStr)
	for _, pattern := range pc.config.ExcludePatterns {
		if strings.Contains(urlLower, strings.ToLower(pattern)) {
			return false
		}
	}

	// 检查是否在目标域
	if pc.engine != nil && pc.engine.HostName != "" {
		parsed, err := url.Parse(urlStr)
		if err != nil {
			return false
		}
		if !strings.EqualFold(parsed.Hostname(), pc.engine.HostName) {
			return false
		}
	}

	return true
}

// calculatePriority 计算URL优先级
func (pc *PassiveCrawler) calculatePriority(urlStr, source string) int {
	priority := 50

	// 来源优先级
	switch source {
	case "xhr", "fetch":
		priority += 30 // API请求高优先级
	case "click_link":
		priority += 20
	case "dom":
		priority += 10
	case "network":
		priority += 15
	}

	// URL特征优先级
	urlLower := strings.ToLower(urlStr)
	if strings.Contains(urlLower, "/api/") {
		priority += 20
	}
	if strings.Contains(urlLower, "action=") || strings.Contains(urlLower, "do=") {
		priority += 15
	}
	if strings.Contains(urlLower, "admin") || strings.Contains(urlLower, "manage") {
		priority += 10
	}

	return priority
}

// submitURL 提交发现的URL
func (pc *PassiveCrawler) submitURL(discovered *DiscoveredURL) {
	if discovered == nil || discovered.URL == "" {
		return
	}

	// 去重
	pc.mu.Lock()
	if pc.discoveredURLs[discovered.URL] {
		pc.mu.Unlock()
		return
	}
	pc.discoveredURLs[discovered.URL] = true
	pc.mu.Unlock()

	atomic.AddInt64(&pc.urlsDiscovered, 1)

	// 发送到队列
	select {
	case pc.pendingURLs <- discovered:
	default:
		// 队列满了，丢弃低优先级
		log.Logger.Debugf("PassiveCrawler: queue full, dropping URL: %s", discovered.URL)
	}
}

// processDiscoveredURL 处理发现的URL
func (pc *PassiveCrawler) processDiscoveredURL(discovered *DiscoveredURL) {
	if discovered == nil || pc.engine == nil {
		return
	}

	atomic.AddInt64(&pc.eventsProcessed, 1)

	// 提交到引擎
	uif := &UrlInfo{
		Url:        discovered.URL,
		SourceType: "passive_" + discovered.Source,
		SourceUrl:  discovered.RefererURL,
		Depth:      1,
	}

	pc.engine.PushStaticUrl(uif)

	// 触发事件
	pc.emitEvent(&CrawlEvent{
		Type:      "url_discovered",
		URL:       discovered.URL,
		Data:      discovered,
		Timestamp: time.Now(),
	})
}

// emitEvent 触发事件
func (pc *PassiveCrawler) emitEvent(event *CrawlEvent) {
	pc.mu.RLock()
	handlers := append([]func(event *CrawlEvent){}, pc.eventListeners...)
	pc.mu.RUnlock()

	for _, handler := range handlers {
		go handler(event)
	}
}

// OnEvent 注册事件处理器
func (pc *PassiveCrawler) OnEvent(handler func(event *CrawlEvent)) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.eventListeners = append(pc.eventListeners, handler)
}

// Stats 获取统计信息
func (pc *PassiveCrawler) Stats() PassiveCrawlerStats {
	return PassiveCrawlerStats{
		URLsDiscovered:  atomic.LoadInt64(&pc.urlsDiscovered),
		RequestsCaught:  atomic.LoadInt64(&pc.requestsCaught),
		EventsProcessed: atomic.LoadInt64(&pc.eventsProcessed),
		PendingCount:    len(pc.pendingURLs),
	}
}

// PassiveCrawlerStats 统计信息
type PassiveCrawlerStats struct {
	URLsDiscovered  int64 `json:"urls_discovered"`
	RequestsCaught  int64 `json:"requests_caught"`
	EventsProcessed int64 `json:"events_processed"`
	PendingCount    int   `json:"pending_count"`
}

// ExtractScriptURLs 从页面脚本中提取URL
func ExtractScriptURLs(page *rod.Page) []string {
	var urls []string

	result, err := page.Eval(`
		() => {
			const urls = [];

			// 提取内联脚本中的URL
			const scripts = document.querySelectorAll('script:not([src])');
			scripts.forEach(script => {
				const content = script.textContent;

				// 匹配 URL 模式
				const urlPatterns = [
					/['"]((https?:)?\/\/[^'"]+)['"]/g,
					/url:\s*['"]([^'"]+)['"]/g,
					/href:\s*['"]([^'"]+)['"]/g,
					/api[_-]?url['":\s]+['"]([^'"]+)['"]/gi,
					/endpoint['":\s]+['"]([^'"]+)['"]/gi
				];

				urlPatterns.forEach(pattern => {
					let match;
					while ((match = pattern.exec(content)) !== null) {
						if (match[1]) urls.push(match[1]);
					}
				});
			});

			// 提取 data 属性中的URL
			const elements = document.querySelectorAll('[data-url], [data-href], [data-api], [data-src]');
			elements.forEach(el => {
				['data-url', 'data-href', 'data-api', 'data-src'].forEach(attr => {
					const val = el.getAttribute(attr);
					if (val && (val.startsWith('/') || val.startsWith('http'))) {
						urls.push(val);
					}
				});
			});

			return [...new Set(urls)];
		}
	`)

	if err != nil {
		return urls
	}

	arr := result.Value.Arr()
	for _, item := range arr {
		if str := item.Str(); str != "" {
			urls = append(urls, str)
		}
	}

	return urls
}

// ExtractAPIEndpoints 从页面中提取API端点
func ExtractAPIEndpoints(page *rod.Page) []string {
	var endpoints []string

	result, err := page.Eval(`
		() => {
			const endpoints = new Set();

			// 从脚本中提取
			const scripts = document.querySelectorAll('script');
			scripts.forEach(script => {
				const content = script.textContent || '';

				// API 路径模式
				const patterns = [
					/['"](\/api\/[^'"]+)['"]/g,
					/['"](\/v[0-9]+\/[^'"]+)['"]/g,
					/['"](\/rest\/[^'"]+)['"]/g,
					/['"](\/graphql[^'"]*)['"]/g,
					/['"]([^'"]*\.json)['"]/g,
					/fetch\s*\(\s*['"]([^'"]+)['"]/g,
					/axios\.[a-z]+\s*\(\s*['"]([^'"]+)['"]/g,
					/\$\.(get|post|ajax)\s*\(\s*['"]([^'"]+)['"]/g
				];

				patterns.forEach(pattern => {
					let match;
					while ((match = pattern.exec(content)) !== null) {
						const url = match[1] || match[2];
						if (url && !url.includes('${')) {
							endpoints.add(url);
						}
					}
				});
			});

			return [...endpoints];
		}
	`)

	if err != nil {
		return endpoints
	}

	arr := result.Value.Arr()
	for _, item := range arr {
		if str := item.Str(); str != "" {
			endpoints = append(endpoints, str)
		}
	}

	return endpoints
}
