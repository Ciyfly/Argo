package engine

import (
	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/static"
	"argo/pkg/utils"
	"argo/pkg/vector"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	HOME_PAGE_FLAG = iota
	NOT_HOME_PAGE_FLAG
	PAGE_TIMEOUT_FLAG
	NOT_PAGE_TIME_FLAG
	PAGE404_FLAG
	RANDPAGE404_FLAG
	TIMEOUT_PAHE
)

type EngineInfo struct {
	BrowserList        []*rod.Browser
	OptionsList        []*launcher.Launcher
	Mutex              sync.Mutex
	FirstPageCloseChan chan bool
	Target             string
	Host               string
	HostName           string
	TabCount           int
	MaxRetries         int
	Page404Samples     []vector.Vector
	Page404Dict        map[string]int
	httpClient         *http.Client
	resourceVisited    map[string]struct{}
	resourceMu         sync.Mutex

	// P0优化: 浏览器池
	BrowserPool *BrowserPool
	// P0优化: 自适应并发控制
	Autoscaler *Autoscaler
	// P0优化: 响应缓存
	ResponseCache *ResponseCache
	// P0优化: 资源过滤器
	ResourceFilter *ResourceFilter

	// P1优化: 增强反检测
	EnhancedStealth *EnhancedStealth
	// P1优化: 智能表单填充
	SmartFormFiller *SmartFormFiller
	// P1优化: 被动爬取
	PassiveCrawler *PassiveCrawler
	// P1优化: 双引擎架构
	DualEngine *DualEngine

	// P2优化: JS静态分析
	JSAnalyzer *JSAnalyzer
	// P2优化: 智能深度控制
	SmartDepthController *SmartDepthController
	// P2优化: 分布式协调器
	DistributedCoordinator *DistributedCoordinator
	// P2优化: 浏览器请求限速器
	BrowserRateLimiter *BrowserRateLimiter
	// P3优化: WebSocket 追踪器
	WebSocketTracker *WebSocketTracker

	Scheduler *Scheduler

	ResultHtmlData *HtmlData
	ResultList     []*PendingUrl
	ResultQueue    chan *PendingUrl
	ResultSinks    map[string]ResultSink

	PendingNormalizeQueue   chan *PendingUrl
	NormalizeCloseChan      chan int
	NormalizeCloseChanFlag  bool
	normalizeResultMu       sync.RWMutex
	NormalizeationResultMap map[string]int
	normalizeStaticMu       sync.RWMutex
	NormalizeationStaticMap map[string]int

	Interactions    []Interaction
	PageMiddlewares []PageMiddleware
	PagesProcessed  int64
	UrlsDropped     int64
	TabsTimeout     int64
	timeoutReasonMu sync.Mutex
	TimeoutReasons  map[string]int

	eventHandlersMu sync.RWMutex
	eventHandlers   []func(EngineEvent)

	loginOnce    sync.Once
	loginOnceErr error
}

type MetricsSummary struct {
	Target          string                 `json:"target"`
	PagesProcessed  int64                  `json:"pages_processed"`
	UrlsDropped     int64                  `json:"urls_dropped"`
	TabsTimeout     int64                  `json:"tabs_timeout"`
	ResultCount     int                    `json:"result_count"`
	RateLimitStats  *BrowserRateLimitStats `json:"rate_limit_stats,omitempty"`
	DualEngineStats *DualEngineStats       `json:"dual_engine_stats,omitempty"`
	WebSocketStats  *WebSocketStats        `json:"websocket_stats,omitempty"`
}

type EngineEvent struct {
	Type      string                 `json:"type"`
	Target    string                 `json:"target"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

type UrlInfo struct {
	Url        string
	Canonical  string
	Hash       string
	Retries    int
	SourceType string
	Match      string
	SourceUrl  string
	Depth      int
}

func InitEngine(target string) *EngineInfo {
	// 初始化 js注入插件
	inject.LoadScript()
	// 初始化 登录插件
	login.InitLoginAuto()
	// 初始化静态资源过滤
	InitFilter()
	// 初始化浏览器
	engineInfo := InitEngineInfo(target)

	// P0优化: 初始化浏览器池
	poolCfg := DefaultBrowserPoolConfig()
	engineInfo.BrowserPool = NewBrowserPool(poolCfg)

	// P0优化: 初始化自适应并发控制器
	asCfg := DefaultAutoscalerConfig()
	asCfg.MaxConcurrency = conf.GlobalConfig.BrowserConf.TabCount
	if asCfg.MaxConcurrency <= 0 {
		asCfg.MaxConcurrency = 5
	}
	engineInfo.Autoscaler = NewAutoscaler(asCfg)

	// P0优化: 初始化响应缓存
	cacheCfg := DefaultResponseCacheConfig()
	engineInfo.ResponseCache = NewResponseCache(cacheCfg)

	// P0优化: 初始化资源过滤器
	engineInfo.ResourceFilter = NewResourceFilter()

	// P1优化: 初始化增强反检测
	engineInfo.EnhancedStealth = NewEnhancedStealth()

	// P1优化: 初始化智能表单填充
	engineInfo.SmartFormFiller = NewSmartFormFiller()

	// P1优化: 初始化被动爬取
	passiveCfg := DefaultPassiveCrawlerConfig()
	engineInfo.PassiveCrawler = NewPassiveCrawler(engineInfo, passiveCfg)
	engineInfo.PassiveCrawler.Start()

	// P1优化: 初始化双引擎架构
	dualEngineCfg := DefaultDualEngineConfig()
	// 应用配置
	if conf.GlobalConfig.DualEngineConf.Enabled {
		dualEngineCfg.Enabled = true
	}
	if conf.GlobalConfig.DualEngineConf.StandardWorkers > 0 {
		dualEngineCfg.StandardEngineWorkers = conf.GlobalConfig.DualEngineConf.StandardWorkers
	}
	engineInfo.DualEngine = NewDualEngine(engineInfo, dualEngineCfg)
	engineInfo.DualEngine.Start()

	// P2优化: 初始化JS分析器
	jsAnalyzerCfg := DefaultJSAnalyzerConfig()
	engineInfo.JSAnalyzer = NewJSAnalyzer(jsAnalyzerCfg)

	// P2优化: 初始化智能深度控制
	depthCfg := DefaultSmartDepthConfig()
	engineInfo.SmartDepthController = NewSmartDepthController(depthCfg)

	// P2优化: 初始化分布式协调器(可选，根据配置启用)
	if conf.GlobalConfig.DistributedMode {
		distCfg := DefaultDistributedConfig()
		engineInfo.DistributedCoordinator = NewDistributedCoordinator(engineInfo, distCfg)
		engineInfo.DistributedCoordinator.Start()
	}

	// P2优化: 初始化浏览器限速器
	browserRateCfg := DefaultBrowserRateLimitConfig()
	if conf.GlobalConfig.RateLimitConf.Enabled {
		browserRateCfg.Enabled = true
	}
	if conf.GlobalConfig.RateLimitConf.BaseIntervalMs > 0 {
		browserRateCfg.BaseInterval = time.Duration(conf.GlobalConfig.RateLimitConf.BaseIntervalMs) * time.Millisecond
	}
	if conf.GlobalConfig.RateLimitConf.MinIntervalMs > 0 {
		browserRateCfg.MinInterval = time.Duration(conf.GlobalConfig.RateLimitConf.MinIntervalMs) * time.Millisecond
	}
	if conf.GlobalConfig.RateLimitConf.MaxIntervalMs > 0 {
		browserRateCfg.MaxInterval = time.Duration(conf.GlobalConfig.RateLimitConf.MaxIntervalMs) * time.Millisecond
	}
	engineInfo.BrowserRateLimiter = NewBrowserRateLimiter(browserRateCfg)

	// P3优化: 初始化 WebSocket 追踪器
	wsCfg := DefaultWebSocketConfig()
	if conf.GlobalConfig.WebSocketConf.Enabled {
		wsCfg.Enabled = true
	}
	if conf.GlobalConfig.WebSocketConf.MaxConnections > 0 {
		wsCfg.MaxConnections = conf.GlobalConfig.WebSocketConf.MaxConnections
	}
	if conf.GlobalConfig.WebSocketConf.MaxMessagesPerConn > 0 {
		wsCfg.MaxMessagesPerConn = conf.GlobalConfig.WebSocketConf.MaxMessagesPerConn
	}
	if conf.GlobalConfig.WebSocketConf.CaptureMessages {
		wsCfg.CaptureMessages = true
	}
	engineInfo.WebSocketTracker = NewWebSocketTracker(wsCfg)
	engineInfo.WebSocketTracker.Start()

	// 初始化泛化模块
	engineInfo.InitNormalize()
	// 初始化 结果处理模块
	engineInfo.InitResultHandler()
	// 初始化调度器
	engineInfo.InitScheduler()
	engineInfo.InitInteractions()
	engineInfo.InitPipeline()
	return engineInfo
}

var leaklessHintOnce sync.Once

func NewBrowserOptions() *launcher.Launcher {
	options := launcher.New().NoSandbox(true).Headless(true)
	disableLeakless := conf.GlobalConfig.BrowserConf.DisableLeakless
	if runtime.GOOS == "windows" && !disableLeakless {
		leaklessHintOnce.Do(func() {
			log.Logger.Infof("Windows 环境默认启用 leakless，如被杀软拦截 leakless.exe 请暂时关闭杀软或将其加入白名单。")
		})
	}
	if disableLeakless {
		log.Logger.Debug("根据配置关闭 leakless")
		options = options.Leakless(false)
	}
	// 指定chrome浏览器路径
	if conf.GlobalConfig.BrowserConf.Chrome != "" {
		log.Logger.Infof("chrome path: %s", conf.GlobalConfig.BrowserConf.Chrome)
		options.Bin(conf.GlobalConfig.BrowserConf.Chrome)
	}
	// 禁用所有提示防止阻塞 浏览器
	options = options.Append("disable-infobars", "")
	options = options.Append("disable-extensions", "")
	options = options.Append("new-window", "0")
	options = options.Append("profile-directory", "Default")
	options.Set("disable-web-security")
	options.Set("allow-running-insecure-content")
	options.Set("reduce-security-for-testing")
	options.Set("ignore-certificate-errors")
	options.Set("disable-popup-blocking")
	options.Set("disable-gpu")
	if conf.GlobalConfig.BrowserConf.UnHeadless || conf.GlobalConfig.Dev {
		options = options.Delete("--headless")
		// browser = browser.SlowMotion(time.Duration(conf.GlobalConfig.AutoConf.Slow) * time.Second)
	}
	if conf.GlobalConfig.BrowserConf.Proxy != "" {
		proxyURL, err := url.Parse(conf.GlobalConfig.BrowserConf.Proxy)
		if err != nil {
			log.Logger.Fatal("proxy err:", err)
		}
		options.Proxy(proxyURL.String())
	}
	// windows下 使用单进程 防止多个cmd窗口弹出
	if runtime.GOOS == "windows" {
		options = options.Set("single-process")
	}
	options.Set("", "about:blank")
	return options
}

func InitEngineInfo(target string) *EngineInfo {
	firstPageCloseChan := make(chan bool, 1)
	u, _ := url.Parse(target)
	return &EngineInfo{
		FirstPageCloseChan: firstPageCloseChan,
		Target:             target,
		Host:               u.Host,
		HostName:           u.Hostname(),
		Page404Dict:        make(map[string]int),
		httpClient:         &http.Client{Timeout: 10 * time.Second},
		resourceVisited:    make(map[string]struct{}),
	}
}

func (ei *EngineInfo) InitScheduler() {
	ei.Scheduler = NewScheduler(ei)
	ei.MaxRetries = conf.GlobalConfig.BrowserConf.MaxRetries
	if ei.MaxRetries <= 0 {
		ei.MaxRetries = 2
	}
	ei.Scheduler.Start()
}

func (ei *EngineInfo) Start() error {
	if conf.GlobalConfig.BrowserConf.Proxy != "" {
		log.Logger.Debugf("proxy: %s", conf.GlobalConfig.BrowserConf.Proxy)
	}
	log.Logger.Debugf("tab timeout: %ds", conf.GlobalConfig.BrowserConf.TabTimeout)
	log.Logger.Debugf("browser timeout: %ds", conf.GlobalConfig.BrowserConf.BrowserTimeout)
	log.Logger.Debugf("tab controller count: %d", conf.GlobalConfig.BrowserConf.TabCount)

	// 这个是 robots.txt|sitemap.xml 爬取解析的
	var metadataWg sync.WaitGroup
	metadataList := static.MetaDataSpider(ei.Target)
	for _, staticUrl := range metadataList {
		metadataWg.Add(1)
		log.Logger.Debugf("metadata parse: %s", staticUrl)
		go func(staticUrl string) {
			defer metadataWg.Done()
			ei.PushStaticUrl(&UrlInfo{Url: staticUrl, SourceType: "metadata parse", SourceUrl: "robots.txt|sitemap.xml", Depth: 0})
		}(staticUrl)
	}
	// 等待 metadata 爬取完成
	metadataWg.Wait()

	for _, seed := range conf.GlobalConfig.SeedList {
		if seed == "" {
			continue
		}
		ei.PushStaticUrl(&UrlInfo{Url: seed, SourceType: "seed", SourceUrl: "seedfile", Depth: 0})
	}
	// 打开第一个tab页面 这里应该提交url管道任务
	// go ei.NewTab(&UrlInfo{Url: ei.Target, Depth: 0, SourceType: "homePage", SourceUrl: "target"}, HOME_PAGE_FLAG)
	ei.PushStaticUrl(&UrlInfo{Url: ei.Target, Depth: 0, SourceType: "homePage", SourceUrl: "target"})
	ei.TimeoutReasons = make(map[string]int)
	ei.Page404Samples = ei.fetch404Samples(3)
	// dev模式的时候不会结束 为了从浏览器界面调试查看需要手动关闭
	if conf.GlobalConfig.Dev {
		log.Logger.Warn("!!! dev mode please ctrl +c kill !!!")
		select {}
	}
	// 结束
	ei.Finish()
	ei.SaveResult()
	return nil
}

func (ei *EngineInfo) PushStaticUrl(uif *UrlInfo) {
	if ei.Scheduler == nil || uif == nil {
		return
	}
	if !ei.prepareUrl(uif) {
		return
	}
	ei.Scheduler.Submit(uif)
	ei.EmitEvent(EngineEvent{Type: "url_submit", Target: uif.Url, Timestamp: time.Now(), Data: map[string]interface{}{"source": uif.SourceType}})
}

func (ei *EngineInfo) prepareUrl(uif *UrlInfo) bool {
	if uif == nil || uif.Url == "" {
		return false
	}
	bases := []string{""}
	if uif.SourceUrl != "" {
		bases = append(bases, uif.SourceUrl)
	}
	if ei.Target != "" {
		bases = append(bases, ei.Target)
	}
	var lastErr error
	for _, base := range bases {
		canonical, err := utils.CanonicalizeURL(uif.Url, base)
		if err != nil {
			lastErr = err
			continue
		}
		uif.Canonical = canonical
		uif.Url = canonical
		uif.Hash = normalizeation(canonical, "GET")
		return true
	}
	if lastErr != nil {
		log.Logger.Debugf("[canonical skip] url=%s err=%v", uif.Url, lastErr)
	}
	return false
}

func (ei *EngineInfo) InitPipeline() {
	order := conf.GlobalConfig.AutoConf.Middlewares
	if len(order) == 0 {
		order = []string{"static", "interaction", "metrics"}
	}
	ei.PageMiddlewares = make([]PageMiddleware, 0, len(order))
	for _, name := range order {
		if factory, ok := middlewareRegistry[name]; ok {
			ei.PageMiddlewares = append(ei.PageMiddlewares, factory())
		} else {
			log.Logger.Warnf("middleware %s not found", name)
		}
	}
	if len(ei.PageMiddlewares) == 0 {
		ei.PageMiddlewares = []PageMiddleware{
			&staticParseMiddleware{},
			&interactionMiddleware{},
			&metricsMiddleware{},
		}
	}
}

func (ei *EngineInfo) runPageMiddlewares(ctx *PageContext) {
	for _, middleware := range ei.PageMiddlewares {
		if ctx != nil && ctx.StageRecorder != nil {
			ctx.StageRecorder("middleware:" + middleware.Name())
		}
		if err := middleware.Handle(ctx); err != nil {
			log.Logger.Warnf("middleware %s err: %s", middleware.Name(), err)
		}
	}
}

func (ei *EngineInfo) RecordPageProcessed(uif *UrlInfo) {
	atomic.AddInt64(&ei.PagesProcessed, 1)
}

func (ei *EngineInfo) MetricsSummary() MetricsSummary {
	summary := MetricsSummary{
		Target:         ei.Target,
		PagesProcessed: atomic.LoadInt64(&ei.PagesProcessed),
		UrlsDropped:    atomic.LoadInt64(&ei.UrlsDropped),
		TabsTimeout:    atomic.LoadInt64(&ei.TabsTimeout),
		ResultCount:    len(ei.ResultList),
	}

	// P2优化: 添加限速统计
	if ei.BrowserRateLimiter != nil {
		stats := ei.BrowserRateLimiter.Stats()
		summary.RateLimitStats = &stats
	}

	// P1优化: 添加双引擎统计
	if ei.DualEngine != nil {
		stats := ei.DualEngine.GetStats()
		summary.DualEngineStats = &stats
	}

	// P3优化: 添加 WebSocket 统计
	if ei.WebSocketTracker != nil {
		stats := ei.WebSocketTracker.GetStats()
		summary.WebSocketStats = &stats
	}

	return summary
}

func (ei *EngineInfo) SubscribeEvents(handler func(EngineEvent)) {
	if handler == nil {
		return
	}
	ei.eventHandlersMu.Lock()
	ei.eventHandlers = append(ei.eventHandlers, handler)
	ei.eventHandlersMu.Unlock()
}

func (ei *EngineInfo) EmitEvent(evt EngineEvent) {
	ei.eventHandlersMu.RLock()
	handlers := append([]func(EngineEvent){}, ei.eventHandlers...)
	ei.eventHandlersMu.RUnlock()
	for _, handler := range handlers {
		go handler(evt)
	}
}

func (ei *EngineInfo) AddBrowser(browser *rod.Browser, options *launcher.Launcher) {
	ei.Mutex.Lock()
	ei.BrowserList = append(ei.BrowserList, browser)
	ei.OptionsList = append(ei.OptionsList, options)
	ei.Mutex.Unlock()
}
func (ei *EngineInfo) DelBrowser(delb *rod.Browser, delo *launcher.Launcher) {
	ei.Mutex.Lock()
	var newBrowserList []*rod.Browser
	var newOptionsList []*launcher.Launcher
	for _, b := range ei.BrowserList {
		if b != delb {
			newBrowserList = append(newBrowserList, b)
		}
	}
	for _, o := range ei.OptionsList {
		if o != delo {
			newOptionsList = append(newOptionsList, o)
		}
	}
	ei.BrowserList = newBrowserList
	ei.OptionsList = newOptionsList
	ei.Mutex.Unlock()
}
func (ei *EngineInfo) Finish() {
	// 1. 任务完成 2. 程序超时
	taskOverChan := make(chan bool, 1)
	go func() {
		// 任务完成
		// 当第一个页面访问完成后才会关闭
		<-ei.FirstPageCloseChan
		log.Logger.Debug("------------------------first page over------------------------")
		// url队列为空 没有新增的url需要测试了
		ei.waitSchedulerIdle()
		log.Logger.Debug("------------------------scheduler idle------------------------")
		taskOverChan <- true
	}()
	select {
	case <-taskOverChan:
		log.Logger.Debug("------------------------task over------------------------")
	// 整体超时
	case <-time.After(time.Duration(conf.GlobalConfig.BrowserConf.BrowserTimeout) * time.Second):
		log.Logger.Warnf("------------------------Argo Exec timeout %ds close exit", conf.GlobalConfig.BrowserConf.BrowserTimeout)
		ei.Close()
	}
	log.Logger.Debug("------------------------Close NormalizeQueue------------------------")
	ei.CloseNormalizeQueue()
	ei.PendingNormalizeQueueEmpty()
}

func (ei *EngineInfo) Close() {
	ei.SaveResult()

	// P3优化: 停止 WebSocket 追踪器
	if ei.WebSocketTracker != nil {
		ei.WebSocketTracker.Stop()
	}

	// P2优化: 停止分布式协调器
	if ei.DistributedCoordinator != nil {
		ei.DistributedCoordinator.Stop()
	}

	// P1优化: 停止被动爬取
	if ei.PassiveCrawler != nil {
		ei.PassiveCrawler.Stop()
	}

	// P0优化: 关闭浏览器池
	if ei.BrowserPool != nil {
		ei.BrowserPool.Close()
	}

	// P0优化: 停止自适应控制器
	if ei.Autoscaler != nil {
		ei.Autoscaler.Stop()
	}

	// P0优化: 关闭响应缓存
	if ei.ResponseCache != nil {
		ei.ResponseCache.Close()
	}

	// 关闭所有浏览器（兼容旧模式）
	for _, b := range ei.BrowserList {
		if b != nil {
			b.Close()
		}
	}
	for _, o := range ei.OptionsList {
		if o != nil {
			o.Kill()
		}
	}

}

func (ei *EngineInfo) waitSchedulerIdle() {
	if ei.Scheduler == nil {
		return
	}
	ei.Scheduler.WaitQueueEmpty()
	ei.Scheduler.WaitTabs()
}

func copyBody(b io.ReadCloser) (r1, r2 io.ReadCloser, err error) {
	if b == nil || b == http.NoBody {
		return http.NoBody, http.NoBody, nil
	}
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(b); err != nil {
		return nil, b, err
	}
	if err = b.Close(); err != nil {
		return nil, b, err
	}
	return io.NopCloser(&buf), io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

func transformHttpHeaders(rspHeaders []*proto.FetchHeaderEntry) http.Header {
	newRspHeaders := http.Header{}
	for _, data := range rspHeaders {
		newRspHeaders.Add(data.Name, data.Value)
	}
	return newRspHeaders
}

func (ei *EngineInfo) compare404Samples(current vector.Vector) float64 {
	if len(ei.Page404Samples) == 0 || current == nil {
		return 0
	}
	var maxSim float64
	for _, sample := range ei.Page404Samples {
		if sample == nil {
			continue
		}
		sim := vector.CosineSimilarity(sample, current)
		if sim > maxSim {
			maxSim = sim
		}
	}
	return maxSim
}

func (ei *EngineInfo) fetch404Samples(count int) []vector.Vector {
	samples := make([]vector.Vector, 0, count)
	client := &http.Client{Timeout: 8 * time.Second}
	for len(samples) < count {
		randURL := ei.Target + "/" + utils.GenRandStr()
		req, err := http.NewRequest(http.MethodGet, randURL, nil)
		if err != nil {
			log.Logger.Debugf("fetch404Samples new request err: %s", err)
			continue
		}
		if conf.GlobalConfig.BrowserConf.Proxy != "" {
			proxyURL, err := url.Parse(conf.GlobalConfig.BrowserConf.Proxy)
			if err == nil {
				transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
				client.Transport = transport
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Logger.Debugf("fetch404Samples err: %s", err)
			continue
		}
		body, _ := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if len(body) == 0 {
			continue
		}
		samples = append(samples, vector.HTMLToVector(string(body)))
	}
	return samples
}

func (ei *EngineInfo) RecordTimeoutReason(stage string) {
	if stage == "" {
		stage = "unknown"
	}
	ei.timeoutReasonMu.Lock()
	defer ei.timeoutReasonMu.Unlock()
	if ei.TimeoutReasons == nil {
		ei.TimeoutReasons = make(map[string]int)
	}
	ei.TimeoutReasons[stage]++
}

func (ei *EngineInfo) getPageInfoWithRetry(page *rod.Page, url string) (*proto.TargetTargetInfo, error) {
	timeout := time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout/2) * time.Second
	if timeout < 5*time.Second {
		timeout = 5 * time.Second
	}
	for attempt := 0; attempt < 2; attempt++ {
		var info *proto.TargetTargetInfo
		var err error
		done := make(chan struct{})
		go func() {
			info, err = utils.GetPageInfoByPage(page)
			close(done)
		}()
		select {
		case <-done:
			if err == nil {
				return info, nil
			}
		case <-time.After(timeout):
			err = fmt.Errorf("getPageInfo timeout after %s", timeout)
		}
		log.Logger.Warnf("getPageInfo retry %s attempt=%d err=%v", url, attempt+1, err)
		page.Reload()
	}
	return utils.GetPageInfoByPage(page)
}
