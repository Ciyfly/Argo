package engine

import (
	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/req"
	"argo/pkg/static"
	"argo/pkg/utils"
	"argo/pkg/vector"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type BrowserInfo struct {
	Page         *rod.Page
	Options      *launcher.Launcher
	PageFlag     int
	TimeoutFlage int
	TabDone      chan bool
	Browser      *rod.Browser
	// P0优化: 浏览器池支持
	PoolInstance *BrowserInstance // 从池中获取的实例
	UsePool      bool             // 是否使用池模式
}

func (ei *EngineInfo) closeTab(bi *BrowserInfo) {
	// P0优化: 池模式下只释放槽位，不关闭浏览器
	if bi.UsePool && bi.PoolInstance != nil {
		// 只关闭页面，释放浏览器实例槽位
		if bi.Page != nil {
			bi.Page.Close()
		}
		if ei.BrowserPool != nil {
			ei.BrowserPool.Release(bi.PoolInstance)
		}
		log.Logger.Debugf("browser pool: released instance, active tabs reduced")
	} else {
		// 旧模式：关闭整个浏览器
		log.Logger.Debugf("browser list size: %d", len(ei.BrowserList))
		if bi.Browser != nil {
			err := bi.Browser.Close()
			if err != nil {
				log.Logger.Errorf("browser close error: %s ", err.Error())
			}
			bi.Options.Kill()
		}
		ei.DelBrowser(bi.Browser, bi.Options)
		log.Logger.Debugf("browser list size: %d", len(ei.BrowserList))
	}

	if bi.TimeoutFlage == NOT_PAGE_TIME_FLAG {
		bi.TabDone <- true
	}
	if bi.PageFlag == HOME_PAGE_FLAG {
		ei.FirstPageCloseChan <- true
	}
}

func (ei *EngineInfo) NormalCloseTab(bi *BrowserInfo) {
	log.Logger.Debugf("NormalCloseTab %d", bi.PageFlag)
	bi.TimeoutFlage = NOT_PAGE_TIME_FLAG
	ei.closeTab(bi)
}
func (ei *EngineInfo) TimeoutCloseTab(bi *BrowserInfo) {
	log.Logger.Debugf("TimeoutCloseTab %d", bi.PageFlag)
	bi.TimeoutFlage = PAGE_TIMEOUT_FLAG
	ei.closeTab(bi)
}

func (ei *EngineInfo) NewTab(uif *UrlInfo, pageFlag int) {

	// init browser
	browser := rod.New()
	// 启动无痕
	if conf.GlobalConfig.BrowserConf.Trace {
		browser = browser.Trace(true)
	}
	options := NewBrowserOptions()
	browser = browser.ControlURL(options.MustLaunch()).MustConnect().NoDefaultDevice().MustIncognito()
	browser.MustIgnoreCertErrors(true)
	ei.AddBrowser(browser, options)
	// hook 请求响应获取所有异步请求
	router := browser.HijackRequests()
	defer router.Stop()
	var reqClient *http.Client
	router.MustAdd("*", func(ctx *rod.Hijack) {
		// 扩展资源拦截范围以提升性能
		resourceType := ctx.Request.Type()
		switch resourceType {
		case proto.NetworkResourceTypeFont:
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		case proto.NetworkResourceTypeImage:
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		case proto.NetworkResourceTypeMedia: // 视频/音频
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		case proto.NetworkResourceTypeTextTrack: // 字幕
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		case proto.NetworkResourceTypeManifest: // 应用清单（已在其他地方处理）
			// 允许通过，但不存储
		}

		// 检查大型静态资源文件（通过 URL 扩展名判断）
		urlStr := ctx.Request.URL().String()
		if shouldBlockByExtension(urlStr) {
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}

		// 防止空指针
		if ctx.Request.Req() != nil && ctx.Request.Req().URL != nil {

			// 优化, 先判断,再组合
			if strings.Contains(ctx.Request.URL().String(), ei.HostName) {
				// P2优化: 自适应限速
				if ei.BrowserRateLimiter != nil {
					ei.BrowserRateLimiter.Wait(ei.HostName)
				}

				var save, body io.ReadCloser
				var saveBytes, reqBytes []byte
				reqBytes, _ = httputil.DumpRequest(ctx.Request.Req(), true)
				// fix 20230320 body nil copy处理会导致 nginx 411 问题 只有当post才进行处理
				// https://open.baidu.com/
				if ctx.Request.Method() == http.MethodPost {
					save, body, _ = copyBody(ctx.Request.Req().Body)
					saveBytes, _ = ioutil.ReadAll(save)
				}
				ctx.Request.Req().Body = body
				if conf.GlobalConfig.BrowserConf.Proxy != "" {
					reqClient = req.GetProxyClient()
				} else {
					reqClient = http.DefaultClient
				}
				ctx.LoadResponse(reqClient, true)

				// P2优化: 根据响应状态码调整限速
				if ei.BrowserRateLimiter != nil {
					statusCode := ctx.Response.Payload().ResponseCode
					if statusCode >= 400 {
						ei.BrowserRateLimiter.RecordFailure(ei.HostName, statusCode)
					} else {
						ei.BrowserRateLimiter.RecordSuccess(ei.HostName)
					}
				}

				// load 后才有响应相关
				if ctx.Response.Payload().ResponseCode == http.StatusNotFound {
					return
				}
				// 先简单的通过关键字匹配 404页面
				if ctx.Response.Payload().Body != nil {
					if static.Match404ResponsePage(reqBytes) {
						log.Logger.Warnf("404 response: %s", ctx.Request.URL().String())
						return
					}

				}
				if _, ok := ei.Page404Dict[ctx.Request.URL().String()]; ok {
					return
				}
				// fix 管道关闭了但是还推数据的问题
				if ei.NormalizeCloseChanFlag {
					return
				}
				pu := &PendingUrl{
					URL:             ctx.Request.URL().String(),
					Method:          ctx.Request.Method(),
					Host:            ctx.Request.Req().Host,
					Headers:         ctx.Request.Req().Header,
					Data:            string(saveBytes),
					ResponseHeaders: transformHttpHeaders(ctx.Response.Payload().ResponseHeaders),
					Status:          ctx.Response.Payload().ResponseCode,
				}

				// update 优化可以不存储请求响应的字符串来优化内存性能
				if !conf.GlobalConfig.NoReqRspStr {
					pu.ResponseBody = utils.EncodeBase64(ctx.Response.Payload().Body)
					pu.RequestStr = utils.EncodeBase64(reqBytes)
				}
				if strings.HasPrefix(pu.URL, "http://"+ei.Host) || strings.HasPrefix(pu.URL, "https://"+ei.Host) {
					ei.pushPendingNormalizeQueue(pu)
				}
			}
		}
		ctx.ContinueRequest(&proto.FetchContinueRequest{})

	})
	go router.Run()

	// tab相关
	tabDone := make(chan bool, 1)
	extendTimeoutCh := make(chan time.Duration, 8)
	var extendClosed int32
	extendTabTimeout := func(d time.Duration) {
		if d <= 0 || atomic.LoadInt32(&extendClosed) == 1 {
			return
		}
		select {
		case extendTimeoutCh <- d:
		default:
			select {
			case extendTimeoutCh <- d:
			default:
			}
		}
	}
	var page *rod.Page
	var pageError error
	var NormalDoneFlag = false
	var TimeoutDoneFlag = false
	var stageValue atomic.Value
	stageValue.Store("init")
	setStage := func(s string) {
		if s == "" {
			s = "unknown"
		}
		stageValue.Store(s)
	}
	getStage := func() string {
		if v := stageValue.Load(); v != nil {
			if stageStr, ok := v.(string); ok && stageStr != "" {
				return stageStr
			}
		}
		return "unknown"
	}
	uif.Retries++
	browserInfo := &BrowserInfo{
		Page:     page,
		Options:  options,
		PageFlag: pageFlag,
		TabDone:  tabDone,
		Browser:  browser,
	}
	var PushUrlWg sync.WaitGroup
	ei.TabCount += 1
	go func() {
		// 创建tab
		if !req.CheckTarget(uif.Url) {
			log.Logger.Debugf("CheckTarget: %s ", uif.Url)
			tabDone <- true
			return
		}
		setStage("open_page")
		page, pageError = browser.Page(proto.TargetCreateTarget{URL: uif.Url})
		if pageError != nil || page == nil {
			log.Logger.Errorf("open page error: %s -> %v", uif.Url, pageError)
			setStage("open_page:failed")
			ei.NormalCloseTab(browserInfo)
			return
		}
		setStage("wait_load")
		page.WaitLoad()
		// 注入反检测脚本
		setStage("inject_stealth")
		// P1优化: 使用增强反检测
		if ei.EnhancedStealth != nil {
			if err := ei.EnhancedStealth.ApplyEnhancedStealth(page); err != nil {
				log.Logger.Debugf("enhanced stealth inject error: %v", err)
			}
		} else if err := ApplyStealthMode(page); err != nil {
			log.Logger.Debugf("stealth inject error: %v", err)
		}
		// P1优化: 附加被动爬取监听
		if ei.PassiveCrawler != nil {
			ei.PassiveCrawler.AttachToPage(page)
		}
		// P3优化: 附加 WebSocket 监听
		if ei.WebSocketTracker != nil {
			ei.WebSocketTracker.SetupPageListeners(page, uif.Url)
		}
		setStage("page_info:init")
		info, err := utils.GetPageInfoByPage(page)
		if err != nil {
			log.Logger.Errorf("GetPageInfoByPage: %s", err.Error())
			ei.NormalCloseTab(browserInfo)
			return
		}
		// 404 页面判断
		setStage("html_snapshot:init")
		if pageFlag == RANDPAGE404_FLAG {
			html, _ := page.HTML()
			if len(html) > 0 {
				ei.Page404Samples = append(ei.Page404Samples, vector.HTMLToVector(html))
			}
			ei.NormalCloseTab(browserInfo)
			return
		}
		html, _ := page.HTML()
		if strings.Contains(info.Title, "404") || static.Match404ResponsePage([]byte(html)) {
			browserInfo.PageFlag = PAGE404_FLAG
			ei.NormalCloseTab(browserInfo)
			return
		}
		setStage("compare_404_vector")
		// 调试模式 手动去操作 停止所有
		if conf.GlobalConfig.Dev {
			// ei.NormalCloseTab(page, pageFlag)
			return
		}

		// 判断页面是不是404页面
		currentPageVector := vector.HTMLToVector(html)
		similarity := ei.compare404Samples(currentPageVector)
		log.Logger.Debugf("404 similarity: %f", similarity)
		if similarity >= 0.92 {
			ei.Page404Dict[uif.Url] = 1
			log.Logger.Debugf("similarity: %f", similarity)
			log.Logger.Debugf("404 page: %s", uif.Url)
			log.Logger.Info("similarity")
			ei.NormalCloseTab(browserInfo)
			return
		}
		if conf.GlobalConfig.TestPlayBack {
			setStage("test_playback_sleep")
			time.Sleep(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second)
			ei.NormalCloseTab(browserInfo)
			return
		}
		log.Logger.Debugf("[ new tab  ]=> %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
		// 注入js dom构建前/后
		setStage("inject_script:before")
		inject.InjectScript(page, 0)
		setStage("inject_script:after")
		inject.InjectScript(page, 1)
		ctx := &PageContext{
			Engine:        ei,
			Page:          page,
			Url:           uif,
			PageFlag:      pageFlag,
			StageRecorder: setStage,
			ExtendTimeout: extendTabTimeout,
		}
		setStage("middlewares:start")
		ei.runPageMiddlewares(ctx)
		setStage("middlewares:done")
		// auto 触发后 获取下当前url
		setStage("page_info:post_middlewares")
		info, err = utils.GetPageInfoByPage(page)
		var currentUrl = ""
		if err != nil {
			log.Logger.Debugf("page timeout:%s  %s", err, uif.Url)
		} else {
			currentUrl = info.URL
		}
		log.Logger.Debugf("dynamic %s parse done", uif.Url)
		// close tab browser
		NormalDoneFlag = true
		if !TimeoutDoneFlag {
			setStage("normal_close_tab")
			ei.NormalCloseTab(browserInfo)
		}
		// 推送下如果 单纯的去修改当前页面url的形式
		// https://spa5.scrape.center/page/1
		if currentUrl != "" {
			setStage("push_patch_urls")
			PushUrlWg.Add(1)
			go func(currentUrl string) {
				defer PushUrlWg.Done()
				ei.PushStaticUrl(&UrlInfo{Url: info.URL, SourceType: "patch", SourceUrl: uif.Url, Depth: uif.Depth + 1})
			}(currentUrl)
		}
		// 所有url提交完成才能结束
		setStage("wait_push_urls")
		PushUrlWg.Wait()
		setStage("wait_push_urls_done")

	}() // 协程

	tabHard := conf.GlobalConfig.BrowserConf.TabTimeout
	if tabHard <= 0 {
		tabHard = 180
	}
	tabSoft := conf.GlobalConfig.BrowserConf.TabSoftTimeout
	if tabSoft <= 0 || tabSoft > tabHard {
		tabSoft = tabHard
	}
	softDuration := time.Duration(tabSoft) * time.Second
	if softDuration <= 0 {
		softDuration = 30 * time.Second
	}
	hardDeadline := time.Now().Add(time.Duration(tabHard) * time.Second)
	tabTimer := time.NewTimer(softDuration)
	defer tabTimer.Stop()

	resetTimer := func(d time.Duration) {
		if d <= 0 {
			d = time.Millisecond
		}
		if !tabTimer.Stop() {
			select {
			case <-tabTimer.C:
			default:
			}
		}
		tabTimer.Reset(d)
	}

	for {
		select {
		case <-tabDone:
			atomic.StoreInt32(&extendClosed, 1)
			log.Logger.Debugf("[close tab ] => %s", uif.Url)
			return
		case extendDur := <-extendTimeoutCh:
			if extendDur <= 0 || atomic.LoadInt32(&extendClosed) == 1 {
				continue
			}
			remaining := hardDeadline.Sub(time.Now())
			if remaining <= 0 {
				resetTimer(time.Millisecond)
				continue
			}
			if extendDur > remaining {
				extendDur = remaining
			}
			if extendDur < time.Millisecond {
				extendDur = time.Millisecond
			}
			resetTimer(extendDur)
		case <-tabTimer.C:
			atomic.StoreInt32(&extendClosed, 1)
			currentStage := getStage()
			log.Logger.Warnf("[timeout tab ] => %s stage=%s", uif.Url, currentStage)
			if !NormalDoneFlag {
				atomic.AddInt64(&ei.TabsTimeout, 1)
				ei.EmitEvent(EngineEvent{Type: "tab_timeout", Target: uif.Url, Timestamp: time.Now(), Data: map[string]interface{}{"stage": currentStage}})
				ei.RecordTimeoutReason(currentStage)
				TimeoutDoneFlag = true
				ei.TimeoutCloseTab(browserInfo)
				if uif.Retries <= ei.MaxRetries {
					log.Logger.Debugf("requeue timeout url %s retries=%d", uif.Url, uif.Retries)
					ei.PushStaticUrl(uif)
				}
			}
			return
		}
	}
}

// shouldBlockByExtension 根据 URL 扩展名判断是否应该拦截资源
// 拦截大型静态资源文件以提升爬取性能
func shouldBlockByExtension(urlStr string) bool {
	// 移除查询参数
	if idx := strings.Index(urlStr, "?"); idx > 0 {
		urlStr = urlStr[:idx]
	}
	// 移除片段
	if idx := strings.Index(urlStr, "#"); idx > 0 {
		urlStr = urlStr[:idx]
	}

	urlLower := strings.ToLower(urlStr)

	// 图片格式
	imageExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico", ".bmp", ".tiff", ".tif", ".avif", ".heic", ".heif"}
	for _, ext := range imageExts {
		if strings.HasSuffix(urlLower, ext) {
			return true
		}
	}

	// 字体格式
	fontExts := []string{".woff", ".woff2", ".ttf", ".otf", ".eot"}
	for _, ext := range fontExts {
		if strings.HasSuffix(urlLower, ext) {
			return true
		}
	}

	// 视频/音频格式
	mediaExts := []string{".mp4", ".webm", ".ogg", ".mp3", ".wav", ".flac", ".aac", ".m4a", ".avi", ".mov", ".wmv", ".mkv", ".m3u8", ".ts"}
	for _, ext := range mediaExts {
		if strings.HasSuffix(urlLower, ext) {
			return true
		}
	}

	// 压缩包/二进制文件
	binaryExts := []string{".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".exe", ".dmg", ".pkg", ".deb", ".rpm", ".apk", ".ipa", ".msi"}
	for _, ext := range binaryExts {
		if strings.HasSuffix(urlLower, ext) {
			return true
		}
	}

	// 文档格式（大文件）
	docExts := []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx"}
	for _, ext := range docExts {
		if strings.HasSuffix(urlLower, ext) {
			return true
		}
	}

	// Source maps（调试文件）
	if strings.HasSuffix(urlLower, ".map") {
		return true
	}

	return false
}

// NewTabWithPool P0优化: 使用浏览器池创建Tab
func (ei *EngineInfo) NewTabWithPool(uif *UrlInfo, pageFlag int) {
	// 如果浏览器池不可用，回退到旧模式
	if ei.BrowserPool == nil {
		ei.NewTab(uif, pageFlag)
		return
	}

	// 记录开始时间用于自适应并发统计
	startTime := time.Now()

	// 从池中获取浏览器实例
	inst, err := ei.BrowserPool.Acquire()
	if err != nil {
		log.Logger.Errorf("acquire browser from pool failed: %v, fallback to NewTab", err)
		ei.NewTab(uif, pageFlag)
		return
	}

	browser := inst.GetBrowser()

	// hook 请求响应获取所有异步请求
	router := browser.HijackRequests()
	defer router.Stop()

	var reqClient *http.Client
	router.MustAdd("*", func(ctx *rod.Hijack) {
		// 使用 ResourceFilter 进行资源过滤
		resourceType := string(ctx.Request.Type())
		urlStr := ctx.Request.URL().String()

		// P0优化: 使用 ResourceFilter 判断是否拦截
		if ei.ResourceFilter != nil && ei.ResourceFilter.ShouldBlock(resourceType, urlStr) {
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}

		// 原有的资源类型拦截逻辑
		switch ctx.Request.Type() {
		case proto.NetworkResourceTypeFont:
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		case proto.NetworkResourceTypeImage:
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		case proto.NetworkResourceTypeMedia:
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		case proto.NetworkResourceTypeTextTrack:
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}

		// 检查大型静态资源文件
		if shouldBlockByExtension(urlStr) {
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}

		// 防止空指针
		if ctx.Request.Req() != nil && ctx.Request.Req().URL != nil {
			if strings.Contains(ctx.Request.URL().String(), ei.HostName) {
				var save, body io.ReadCloser
				var saveBytes, reqBytes []byte
				reqBytes, _ = httputil.DumpRequest(ctx.Request.Req(), true)

				if ctx.Request.Method() == http.MethodPost {
					save, body, _ = copyBody(ctx.Request.Req().Body)
					saveBytes, _ = ioutil.ReadAll(save)
				}
				ctx.Request.Req().Body = body

				if conf.GlobalConfig.BrowserConf.Proxy != "" {
					reqClient = req.GetProxyClient()
				} else {
					reqClient = http.DefaultClient
				}
				ctx.LoadResponse(reqClient, true)

				if ctx.Response.Payload().ResponseCode == http.StatusNotFound {
					return
				}
				if ctx.Response.Payload().Body != nil {
					if static.Match404ResponsePage(reqBytes) {
						log.Logger.Warnf("404 response: %s", ctx.Request.URL().String())
						return
					}
				}
				if _, ok := ei.Page404Dict[ctx.Request.URL().String()]; ok {
					return
				}
				if ei.NormalizeCloseChanFlag {
					return
				}

				pu := &PendingUrl{
					URL:             ctx.Request.URL().String(),
					Method:          ctx.Request.Method(),
					Host:            ctx.Request.Req().Host,
					Headers:         ctx.Request.Req().Header,
					Data:            string(saveBytes),
					ResponseHeaders: transformHttpHeaders(ctx.Response.Payload().ResponseHeaders),
					Status:          ctx.Response.Payload().ResponseCode,
				}

				if !conf.GlobalConfig.NoReqRspStr {
					pu.ResponseBody = utils.EncodeBase64(ctx.Response.Payload().Body)
					pu.RequestStr = utils.EncodeBase64(reqBytes)
				}
				if strings.HasPrefix(pu.URL, "http://"+ei.Host) || strings.HasPrefix(pu.URL, "https://"+ei.Host) {
					ei.pushPendingNormalizeQueue(pu)
				}
			}
		}
		ctx.ContinueRequest(&proto.FetchContinueRequest{})
	})
	go router.Run()

	// tab相关
	tabDone := make(chan bool, 1)
	extendTimeoutCh := make(chan time.Duration, 8)
	var extendClosed int32

	extendTabTimeout := func(d time.Duration) {
		if d <= 0 || atomic.LoadInt32(&extendClosed) == 1 {
			return
		}
		select {
		case extendTimeoutCh <- d:
		default:
			select {
			case extendTimeoutCh <- d:
			default:
			}
		}
	}

	var page *rod.Page
	var pageError error
	var NormalDoneFlag = false
	var TimeoutDoneFlag = false
	var stageValue atomic.Value
	stageValue.Store("init")

	setStage := func(s string) {
		if s == "" {
			s = "unknown"
		}
		stageValue.Store(s)
	}
	getStage := func() string {
		if v := stageValue.Load(); v != nil {
			if stageStr, ok := v.(string); ok && stageStr != "" {
				return stageStr
			}
		}
		return "unknown"
	}

	uif.Retries++
	browserInfo := &BrowserInfo{
		Page:         page,
		Options:      inst.GetLauncher(),
		PageFlag:     pageFlag,
		TabDone:      tabDone,
		Browser:      browser,
		PoolInstance: inst,
		UsePool:      true,
	}

	var PushUrlWg sync.WaitGroup
	ei.TabCount += 1

	go func() {
		// 创建tab
		if !req.CheckTarget(uif.Url) {
			log.Logger.Debugf("CheckTarget: %s ", uif.Url)
			tabDone <- true
			return
		}
		setStage("open_page")
		page, pageError = browser.Page(proto.TargetCreateTarget{URL: uif.Url})
		if pageError != nil || page == nil {
			log.Logger.Errorf("open page error: %s -> %v", uif.Url, pageError)
			setStage("open_page:failed")
			// P0优化: 记录失败
			if ei.Autoscaler != nil {
				ei.Autoscaler.RecordFailure()
			}
			ei.NormalCloseTab(browserInfo)
			return
		}

		// 更新 browserInfo 的 Page
		browserInfo.Page = page

		setStage("wait_load")
		page.WaitLoad()

		// 注入反检测脚本
		setStage("inject_stealth")
		// P1优化: 使用增强反检测
		if ei.EnhancedStealth != nil {
			if err := ei.EnhancedStealth.ApplyEnhancedStealth(page); err != nil {
				log.Logger.Debugf("enhanced stealth inject error: %v", err)
			}
		} else if err := ApplyStealthMode(page); err != nil {
			log.Logger.Debugf("stealth inject error: %v", err)
		}
		// P1优化: 附加被动爬取监听
		if ei.PassiveCrawler != nil {
			ei.PassiveCrawler.AttachToPage(page)
		}
		// P3优化: 附加 WebSocket 监听
		if ei.WebSocketTracker != nil {
			ei.WebSocketTracker.SetupPageListeners(page, uif.Url)
		}

		setStage("page_info:init")
		info, err := utils.GetPageInfoByPage(page)
		if err != nil {
			log.Logger.Errorf("GetPageInfoByPage: %s", err.Error())
			ei.NormalCloseTab(browserInfo)
			return
		}

		// 404 页面判断
		setStage("html_snapshot:init")
		if pageFlag == RANDPAGE404_FLAG {
			html, _ := page.HTML()
			if len(html) > 0 {
				ei.Page404Samples = append(ei.Page404Samples, vector.HTMLToVector(html))
			}
			ei.NormalCloseTab(browserInfo)
			return
		}

		html, _ := page.HTML()
		if strings.Contains(info.Title, "404") || static.Match404ResponsePage([]byte(html)) {
			browserInfo.PageFlag = PAGE404_FLAG
			ei.NormalCloseTab(browserInfo)
			return
		}

		setStage("compare_404_vector")
		if conf.GlobalConfig.Dev {
			return
		}

		// 判断页面是不是404页面
		currentPageVector := vector.HTMLToVector(html)
		similarity := ei.compare404Samples(currentPageVector)
		log.Logger.Debugf("404 similarity: %f", similarity)
		if similarity >= 0.92 {
			ei.Page404Dict[uif.Url] = 1
			log.Logger.Debugf("similarity: %f", similarity)
			log.Logger.Debugf("404 page: %s", uif.Url)
			ei.NormalCloseTab(browserInfo)
			return
		}

		if conf.GlobalConfig.TestPlayBack {
			setStage("test_playback_sleep")
			time.Sleep(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second)
			ei.NormalCloseTab(browserInfo)
			return
		}

		log.Logger.Debugf("[ new tab pool ]=> %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)

		// 注入js dom构建前/后
		setStage("inject_script:before")
		inject.InjectScript(page, 0)
		setStage("inject_script:after")
		inject.InjectScript(page, 1)

		ctx := &PageContext{
			Engine:        ei,
			Page:          page,
			Url:           uif,
			PageFlag:      pageFlag,
			StageRecorder: setStage,
			ExtendTimeout: extendTabTimeout,
		}

		setStage("middlewares:start")
		ei.runPageMiddlewares(ctx)
		setStage("middlewares:done")

		// auto 触发后 获取下当前url
		setStage("page_info:post_middlewares")
		info, err = utils.GetPageInfoByPage(page)
		var currentUrl = ""
		if err != nil {
			log.Logger.Debugf("page timeout:%s  %s", err, uif.Url)
		} else {
			currentUrl = info.URL
		}

		log.Logger.Debugf("dynamic %s parse done", uif.Url)

		// P0优化: 记录成功和延迟
		latencyMs := time.Since(startTime).Milliseconds()
		if ei.Autoscaler != nil {
			ei.Autoscaler.RecordSuccess(latencyMs)
		}

		// close tab browser
		NormalDoneFlag = true
		if !TimeoutDoneFlag {
			setStage("normal_close_tab")
			ei.NormalCloseTab(browserInfo)
		}

		// 推送URL
		if currentUrl != "" {
			setStage("push_patch_urls")
			PushUrlWg.Add(1)
			go func(currentUrl string) {
				defer PushUrlWg.Done()
				ei.PushStaticUrl(&UrlInfo{Url: info.URL, SourceType: "patch", SourceUrl: uif.Url, Depth: uif.Depth + 1})
			}(currentUrl)
		}

		setStage("wait_push_urls")
		PushUrlWg.Wait()
		setStage("wait_push_urls_done")
	}()

	// 超时控制逻辑
	tabHard := conf.GlobalConfig.BrowserConf.TabTimeout
	if tabHard <= 0 {
		tabHard = 180
	}
	tabSoft := conf.GlobalConfig.BrowserConf.TabSoftTimeout
	if tabSoft <= 0 || tabSoft > tabHard {
		tabSoft = tabHard
	}
	softDuration := time.Duration(tabSoft) * time.Second
	if softDuration <= 0 {
		softDuration = 30 * time.Second
	}
	hardDeadline := time.Now().Add(time.Duration(tabHard) * time.Second)
	tabTimer := time.NewTimer(softDuration)
	defer tabTimer.Stop()

	resetTimer := func(d time.Duration) {
		if d <= 0 {
			d = time.Millisecond
		}
		if !tabTimer.Stop() {
			select {
			case <-tabTimer.C:
			default:
			}
		}
		tabTimer.Reset(d)
	}

	for {
		select {
		case <-tabDone:
			atomic.StoreInt32(&extendClosed, 1)
			log.Logger.Debugf("[close tab pool] => %s", uif.Url)
			return
		case extendDur := <-extendTimeoutCh:
			if extendDur <= 0 || atomic.LoadInt32(&extendClosed) == 1 {
				continue
			}
			remaining := hardDeadline.Sub(time.Now())
			if remaining <= 0 {
				resetTimer(time.Millisecond)
				continue
			}
			if extendDur > remaining {
				extendDur = remaining
			}
			if extendDur < time.Millisecond {
				extendDur = time.Millisecond
			}
			resetTimer(extendDur)
		case <-tabTimer.C:
			atomic.StoreInt32(&extendClosed, 1)
			currentStage := getStage()
			log.Logger.Warnf("[timeout tab pool] => %s stage=%s", uif.Url, currentStage)
			if !NormalDoneFlag {
				atomic.AddInt64(&ei.TabsTimeout, 1)
				ei.EmitEvent(EngineEvent{Type: "tab_timeout", Target: uif.Url, Timestamp: time.Now(), Data: map[string]interface{}{"stage": currentStage}})
				ei.RecordTimeoutReason(currentStage)

				// P0优化: 记录超时
				if ei.Autoscaler != nil {
					ei.Autoscaler.RecordTimeout()
				}

				TimeoutDoneFlag = true
				ei.TimeoutCloseTab(browserInfo)
				if uif.Retries <= ei.MaxRetries {
					log.Logger.Debugf("requeue timeout url %s retries=%d", uif.Url, uif.Retries)
					ei.PushStaticUrl(uif)
				}
			}
			return
		}
	}
}
