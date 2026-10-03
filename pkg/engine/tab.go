package engine

import (
	"argo/pkg/conf"
	"argo/pkg/extract"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/playback"
	"argo/pkg/ratelimit"
	"argo/pkg/req"
	"argo/pkg/scope"
	"argo/pkg/static"
	"argo/pkg/utils"
	"argo/pkg/vector"
	"bufio"
	"context"
	"net/http"
	urlpkg "net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/panjf2000/ants/v2"
)

// tab 协程组
var TabWg sync.WaitGroup

// 控制tab的数量
var TabLimit chan int

// TabLimit 关闭flag
var TabLimitCloseFlag bool

func (ei *EngineInfo) closeTab(page *rod.Page, pageFlag int, timeoutFlage int, tabDone chan bool) {
	log.Logger.Debugf("TabLimit  1: %d", len(TabLimit))
	if page != nil {
		e := page.Close()
		if e != nil {
			log.Logger.Debugf("page close error: %s", e.Error())
		}
	}
	log.Logger.Debugf("TabLimit  2: %d", len(TabLimit))
	if pageFlag == HOME_PAGE_FLAG {
		ei.FirstPageCloseChan <- true
	}
}

func (ei *EngineInfo) NormalCloseTab(page *rod.Page, pageFlag int, tabDone chan bool) {
	ei.closeTab(page, pageFlag, NOT_PAGE_TIME_FLAG, tabDone)
}

func (ei *EngineInfo) TimeoutCloseTab(page *rod.Page, pageFlag int, tabDone chan bool) {
	ei.closeTab(page, pageFlag, PAGE_TIMEOUT_FLAG, tabDone)
}

// waitForPageLoad 等待页面可用。
//
// 历史实现每页固定耗 4 秒以上：WaitRequestIdle(2s) + WaitDOMStable(2s)
// 是「最多等 2 秒」而不是「等满 2 秒」，但随后还有一个 JS 检查循环
// （最多 5 次 × 1 秒 sleep），即使页面 0.3 秒就绪也要等 4~5 秒。
// 90 个页面就是 400 秒的纯等待，是整体耗时的大头。
//
// 现在改为事件驱动：readyState 一到 interactive/complete 立即返回；
// WaitRequestIdle 用短超时兜底（SPA 页面可能持续发请求，不能死等）。
// 判定标准不变——依旧要求 DOM 稳定、无加载指示，只是不再用 sleep 凑时间。
func (ei *EngineInfo) waitForPageLoad(page *rod.Page) bool {
	loadedChan := make(chan bool, 1)

	go func() {
		defer func() {
			// rod 的 Wait 系列在页面关闭/导航时会 panic，兜住避免拖垮整个爬虫
			if r := recover(); r != nil {
				loadedChan <- true
			}
		}()

		// 等待 load 事件（页面自身的加载完成信号）
		page.WaitLoad()

		// 网络空闲：短超时兜底。SPA 页面可能持续发请求，
		// 死等 2 秒以上没有意义——交互阶段自己会等变化。
		page.WaitRequestIdle(800*time.Millisecond, nil, nil, nil)()

		// DOM 稳定：同样用短超时兜底
		_ = page.WaitDOMStable(500*time.Millisecond, 0.1)

		// 快速确认页面可用：readyState 达标即通过，
		// 最多轮询 10 次 × 100ms（总 1 秒），不再用 1 秒 sleep 凑次数
		js := `() => document.readyState === "complete" || document.readyState === "interactive"`
		for i := 0; i < 10; i++ {
			result, err := page.Eval(js)
			if err == nil && result.Value.Bool() {
				loadedChan <- true
				return
			}
			time.Sleep(100 * time.Millisecond)
		}

		// 多次未达标也继续：后续交互有自己的等待与容错
		loadedChan <- true
	}()

	select {
	case <-loadedChan:
		return true
	case <-time.After(20 * time.Second):
		log.Logger.Warn("Page load timed out")
		return false
	}
}

func (ei *EngineInfo) NewTab(uif *UrlInfo, pageFlag int) {
	tabDone := make(chan bool, 1)
	var page *rod.Page
	var pageError error
	var NormalDoneFlag = false
	var TimeoutDoneFlag = false
	if TabLimitCloseFlag {
		return
	}
	// 单页进度跟踪：驱动 idle 超时（页面有进展就不杀），代替固定 tab 超时
	tabProgress := &tabProgressTracker{}
	tabProgress.Mark()
	var PushUrlWg sync.WaitGroup
	// 入队函数：优先走 tabPool（并发 100），池关闭/异常时同步直推。
	//
	// 必须有同步直推兜底：页面交互（点击/表单提交）耗时可能超过
	// tab 超时，此时外层会关闭页面并结束本协程，tabPool 被 Release，
	// 之后 Invoke 全部报 "this pool has been closed"——
	// Auto 收集到的链接一条都进不了队列（实测一次丢 26 个 interact 链接）。
	//
	// 先声明再赋值：闭包要引用 tabPool，而 tabPool 在下一行才创建。
	var pushURL func(*UrlInfo) bool
	tabPool, _ := ants.NewPoolWithFunc(100, func(data interface{}) {
		urlInfo := data.(*UrlInfo)
		PushUrlQueue(urlInfo)
		PushUrlWg.Done()
	})
	pushURL = func(uif *UrlInfo) bool {
		tabProgress.Mark()
		if tabPool != nil {
			if err := tabPool.Invoke(uif); err == nil {
				return true
			}
		}
		PushUrlQueue(uif)
		return true
	}
	defer tabPool.Release()

	domLoadedChan := make(chan bool, 1)

	go func() {
		// 创建tab
		page, pageError = ei.Browser.Page(proto.TargetCreateTarget{URL: uif.Url})
		if pageError != nil || page == nil {
			tabDone <- true
			return
		}
		// web 控制台跟随画面（无 hook 时零开销）
		if OnTabOpen != nil {
			OnTabOpen(page)
		}
		// 注入策略（两层）：
		// 1. EvalOnNewDocument（IIFE 脚本源码）——对本 tab 的每次后续导航生效，
		//    document 起点执行，早于框架脚本：listener 钩子能捕获首屏绑定的
		//    事件（SPA div @click），蜘蛛随首帧就位。
		//    注意 rod 该接口按"语句"执行，必须 IIFE 包裹（函数表达式会被丢弃）。
		// 2. 当前文档兜底：页面以 URL 创建，首个文档不经过 addScript，
		//    用短重试循环立即注入（上下文一活就装）。
		_, _ = page.EvalOnNewDocument(inject.ListenerHookScript())
		_, _ = page.EvalOnNewDocument(inject.StealthScript())
		spiderWanted := conf.GlobalConfig.BrowserConf.UnHeadless || conf.GlobalConfig.Dev ||
			conf.GlobalConfig.BrowserConf.WaitLogin || conf.GlobalConfig.WebConsole
		if spiderWanted {
			if script := inject.SpiderOverlayScript(); script != "" {
				_, _ = page.EvalOnNewDocument(script)
			}
		}
		go func() {
			hookOK, spiderOK := false, !spiderWanted
			for i := 0; i < 40 && !(hookOK && spiderOK); i++ { // 最多重试 ~6s
				if !hookOK {
					if _, err := page.Eval(inject.ListenerHookJS()); err == nil {
						// 反爬伪装同轮带上（同一文档一次性装完）
						_, _ = page.Eval(inject.StealthScript())
						hookOK = true
					}
				}
				if !spiderOK {
					if js := inject.SpiderOverlayJS(); js != "" {
						if _, err := page.Eval(js); err == nil {
							spiderOK = true
						}
					} else {
						spiderOK = true
					}
				}
				if hookOK && spiderOK {
					return
				}
				time.Sleep(150 * time.Millisecond)
			}
		}()

		// 等待页面加载
		if ei.waitForPageLoad(page) {
			domLoadedChan <- true
		} else {
			tabDone <- true
			return
		}

		info, err := utils.GetPageInfoByPage(page)
		if err != nil {
			tabDone <- true
			return
		}
		ei.IncrTabCount()
		// 404 页面判断
		if pageFlag == RANDPAGE404_FLAG {
			html, _ := page.HTML()
			ei.SetPage404Vector(vector.HTMLToVector(html))
			ei.NormalCloseTab(page, pageFlag, tabDone)
			return
		}
		html, _ := page.HTML()
		if strings.Contains(info.Title, "404") || static.Match404ResponsePage([]byte(html)) {
			ei.NormalCloseTab(page, PAGE404_FLAG, tabDone)
			return
		}
		// 调试模式 手动去操作 停止所有
		if conf.GlobalConfig.Dev {
			return
		}
		// 判断页面是不是404页面
		currentPageVector := vector.HTMLToVector(html)
		similarity := vector.CosineSimilarity(ei.GetPage404Vector(), currentPageVector)
		log.Logger.Debugf("similarity: %f", similarity)
		if similarity > 0.95 {
			ei.MarkPage404(uif.Url)
			log.Logger.Debugf("similarity: %f", similarity)
			log.Logger.Debugf("404 page: %s", uif.Url)
			log.Logger.Info("similarity")
			ei.NormalCloseTab(page, pageFlag, tabDone)
			return
		}
		if pageFlag == HOME_PAGE_FLAG {
			// 人工登录模式：首页加载后暂停，等人完成登录（验证码/短信/扫码）
			// 后回终端按回车。等待期间持续保活进度，防止 idle/超时误杀首页。
			if conf.GlobalConfig.BrowserConf.WaitLogin {
				waitManualLogin(tabProgress)
			}
			//  执行headless脚本 只有访问第一个页面的时候才会执行
			if conf.GlobalConfig.PlaybackPath != "" {
				log.Logger.Debugf("run playback script: %s", conf.GlobalConfig.PlaybackPath)
				playback.Run(conf.GlobalConfig.PlaybackPath, page)
			}
		}
		if conf.GlobalConfig.TestPlayBack {
			time.Sleep(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second)
			ei.NormalCloseTab(page, pageFlag, tabDone)
			return
		}
		log.Logger.Debugf("[ new tab  ]=> %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
		// 有头模式 / web 控制台模式注入蜘蛛爬行动画叠加层（closed shadow，
		// 对爬取逻辑完全隐形；无头下截图同样能渲染 CSS 动画）
		if conf.GlobalConfig.BrowserConf.UnHeadless || conf.GlobalConfig.Dev ||
			conf.GlobalConfig.BrowserConf.WaitLogin || conf.GlobalConfig.WebConsole {
			inject.InjectSpiderOverlay(page)
		}
		// 注入js dom构建前
		inject.InjectScript(page, 0)
		// 判断是否需要登录 需要的话进行自动化尝试登录
		login.GlobalLoginAutoData.Handler(page)
		// 注入js dom构建后
		inject.InjectScript(page, 1)

		// 静态解析下dom 爬取一些url
		staticUrlList := static.ParseDom(page)
		log.Logger.Debugf("static %s parse count: %d", uif.Url, len(staticUrlList))
		if staticUrlList != nil {
			for _, staticUrl := range staticUrlList {
				PushUrlWg.Add(1)
				data := &UrlInfo{Url: staticUrl, SourceType: "static parse", SourceUrl: uif.Url, Depth: uif.Depth + 1}
				tabProgress.Mark()
				_ = tabPool.Invoke(data)
			}
		}
		// 执行自动化触发事件 输入 点击等 auto
		hrefList := inject.Auto(page, tabProgress.Mark)

		// auto 触发后 获取下当前url
		//
		// 注意顺序：必须先把 Auto 收集到的 hrefList 全部入队，
		// 再判断页面是否已失效。
		//
		// Auto 里的表单提交/点击都可能触发页面跳转，跳转后页面对象失效，
		// 这里的 GetPageInfoByPage 就会报错。之前是先取信息、失败就直接
		// return，Auto 辛苦收集到的链接全部被丢弃——实测首页一次就丢了
		// 26 个 interact 链接（reveal/wizard/menu/tab/lazy/unlock/crumb 全没）。
		info, infoErr := utils.GetPageInfoByPage(page)

		log.Logger.Debugf("dynamic %s parse count: %d", uif.Url, len(staticUrlList))
		pushed := 0
		for _, staticUrl := range hrefList {
			data := &UrlInfo{Url: staticUrl, SourceType: "auto js", SourceUrl: uif.Url, Depth: uif.Depth + 1}
			if pushURL(data) {
				PushUrlWg.Add(1)
				pushed++
			}
		}
		log.Logger.Debugf("auto js push: %d/%d", pushed, len(hrefList))

		// 推送下如果 单纯的去修改当前页面url的形式
		if infoErr == nil && info != nil && info.URL != "" {
			PushUrlWg.Add(1)
			data := &UrlInfo{Url: info.URL, SourceType: "patch", SourceUrl: uif.Url, Depth: uif.Depth + 1}
			_ = tabPool.Invoke(data)
		} else if infoErr != nil {
			log.Logger.Debugf("page invalid after auto: %s %s (但已收集的 %d 条链接照常入队)",
				infoErr, uif.Url, len(hrefList))
		}
		// 所有url提交完成才能结束
		PushUrlWg.Wait()
		tabDone <- true
	}() // 协程

	// 等待DOM加载完成，最多等待25秒（给waitForPageLoad多5秒的缓冲时间）
	select {
	case <-domLoadedChan:
		// DOM加载完成，继续执行
	case <-time.After(25 * time.Second):
		// 如果等待DOM加载的过程中超过25秒，直接关闭页面
		log.Logger.Warnf("[timeout during page loading] => %s", uif.Url)
		TimeoutDoneFlag = true
		ei.TimeoutCloseTab(page, pageFlag, tabDone)
		return
	}

	// DOM加载完成后，等待页面处理完成。
	//
	// 超时是双层的，都不再用固定的 tabTimeout 杀交互中的页面：
	//  1. idle 超时：页面静默超过 tab_idle 秒（无新链接/无进展）就杀——
	//     挂起页面快速回收，快页面不受影响；
	//  2. 硬上限：inject.InteractionBudget() + 10s，防进度信号异常时的兜底。
	idleKill := make(chan struct{})
	go func() {
		idle := time.Duration(conf.GlobalConfig.AutoConf.TabIdle) * time.Second
		if idle <= 0 {
			idle = 10 * time.Second
		}
		for {
			select {
			case <-tabDone:
				return
			default:
			}
			time.Sleep(1 * time.Second)
			if tabProgress.IdleFor() > idle {
				close(idleKill)
				return
			}
		}
	}()
	select {
	case <-tabDone:
		log.Logger.Debugf("[close tab ] => %s", uif.Url)
		NormalDoneFlag = true
		if !TimeoutDoneFlag {
			ei.NormalCloseTab(page, pageFlag, tabDone)
		}
	case <-idleKill:
		log.Logger.Warnf("[idle tab    ] => %s", uif.Url)
		if !NormalDoneFlag {
			TimeoutDoneFlag = true
			ei.TimeoutCloseTab(page, pageFlag, tabDone)
		}
	case <-time.After(inject.InteractionBudget() + 10*time.Second):
		log.Logger.Warnf("[timeout tab ] => %s", uif.Url)
		if !NormalDoneFlag {
			TimeoutDoneFlag = true
			ei.TimeoutCloseTab(page, pageFlag, tabDone)
		}
	}
}

// waitManualLogin 阻塞等待人工在浏览器里完成登录，终端回车后返回。
//
// stdin 不是终端（管道/CI）时跳过等待——否则无人值守场景会永久挂死；
// 等待期间每秒 Mark 一次进度，首页不会被 idle 超时杀掉。
func waitManualLogin(tabProgress *tabProgressTracker) {
	if !stdinIsTerminal() {
		log.Logger.Warn("[wait-login] stdin 非终端（管道/CI？），跳过人工登录等待，回退自动登录")
		return
	}
	log.Logger.Warn("[wait-login] 请在浏览器中完成登录（验证码/短信均可），然后回到终端按回车继续爬取...")
	enterPressed := make(chan struct{})
	go func() {
		defer close(enterPressed)
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return // EOF 等：不再等待
			}
			if strings.TrimSpace(line) == "" {
				return
			}
			// 输入了非空内容（可能是误触）：继续等下一次回车
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-enterPressed:
			tabProgress.Mark()
			log.Logger.Info("[wait-login] 继续爬取")
			return
		case <-ticker.C:
			tabProgress.Mark()
		}
	}
}

// stdinIsTerminal 判断标准输入是否为交互终端。
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// 接收所有静态url 来处理
var UrlsQueue chan *UrlInfo
var UrlsQueueCloseFlag bool
var TabQueue chan *UrlInfo
var PendUrlQueue chan *UrlInfo

func (ei *EngineInfo) InitTabPool(ctx context.Context) {
	UrlsQueue = make(chan *UrlInfo, 10000)
	TabQueue = make(chan *UrlInfo, conf.GlobalConfig.BrowserConf.TabCount)
	TabLimit = make(chan int, conf.GlobalConfig.BrowserConf.TabCount)
	// 导航限速器（rate/hostrate 为 0 时完全直通）
	ratelimit.Init(conf.GlobalConfig.BrowserConf.RateLimit, conf.GlobalConfig.BrowserConf.HostRateLimit)
	for i := 1; i < conf.GlobalConfig.BrowserConf.TabCount; i++ {
		go ei.PendUrlWork(ctx)
	}
	go ei.TabWork(ctx)
}

func CloseUrlQueue() {
	UrlsQueueCloseFlag = true
	close(UrlsQueue)
}

func PushUrlQueue(uif *UrlInfo) {
	if UrlsQueueCloseFlag {
		return
	}
	// 断点续爬：登记待处理影子集合，PendUrlWork 消费后移除
	RegisterPendingShadow(uif)
	UrlsQueue <- uif
}

func PushTabQueue(uif *UrlInfo) {
	log.Logger.Debugf("PushTabQueue url: %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
	TabQueue <- uif
}

func (ei *EngineInfo) TabWork(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case TabLimit <- 1:
			// 从队列中获取一个 URL 对象并创建新协程去处理它
			select {
			case <-ctx.Done():
				log.Logger.Info("-------------close TabWork ctx------------------")
				return
			case uif, ok := <-TabQueue:
				if !ok {
					return
				}
				// scope 外的直接不进行访问
				if !scope.IsInScope(uif.Url) {
					scope.RecordOutScope(uif.Url)
					<-TabLimit
					continue
				}
				if uif.Depth > conf.GlobalConfig.BrowserConf.MaxDepth {
					log.Logger.Debugf("[ Max Depth] => %s depth: %d", uif.Url, uif.Depth)
					// 将当前并发数减 1
					<-TabLimit
					continue
				}
				TabWg.Add(1)
				go func() {
					defer func() {
						// 当前tab done 继续推送url
						TabWg.Done()
						<-TabLimit
					}()
					// 导航限速：只在「我们主动开 tab」这一层节流（0 = 不限速直通）。
					// 持有 TabLimit 令牌时等待是安全的：并发上限不变，只是推迟放行。
					ratelimit.WaitNavigation(ctx, uif.Url)
					log.Logger.Debugf("[ new tab  ]=> %s", uif.Url)
					// 每个目标开始前清空上一轮的结果，避免多目标时结果串到下一个目标
					if uif.SourceType == "homePage" {
						ResetResult()
						ei.NewTab(uif, HOME_PAGE_FLAG)
					} else {
						ei.NewTab(uif, NOT_HOME_PAGE_FLAG)
					}
				}()
			}
		default:
			// TabQueue 暂空但令牌可用：短轮询而不是睡 1 秒。
			// 之前每个任务平均多等 0.5 秒，100 个页面就是 50 秒纯调度延迟；
			// 而且睡太久会让 tab 池出现「忙的忙死、闲的闲等」的波浪。
			// 20ms 的空转轮询成本可忽略（无锁、无系统调用）。
			time.Sleep(20 * time.Millisecond)
			continue
		}
	}
}

func (ei *EngineInfo) PendUrlWork(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case uif, ok := <-UrlsQueue:
			if !ok {
				return
			}
			ConsumePendingShadow(uif.Url)
			if uif.Url == "" {
				continue
			}
			// scope 外的 URL 记录后丢弃
			if strings.Contains(uif.Url, "http") && !scope.IsInScope(uif.Url) {
				scope.RecordOutScope(uif.Url)
				continue
			}
			if filterStaticPendUrl(uif.Url) {
				// 静态资源不会去打开js
				continue
			} // 泛化后不重复才会请求

			if !urlIsExists(uif.Url) {
				// hybrid 引擎：depth>=1 的文档页走 Go 抓取（fetcher.go），
				// 首页/空壳页/非文档 URL 照旧浏览器/预检分流
				if hybridEnabled() && uif.Depth >= 1 && extract.IsDocumentURL(uif.Url) {
					if FetchDocumentPage(uif) {
						// 空壳页升级浏览器
						PushTabQueue(uif)
					}
					continue
				}
				// 无文档后缀的 URL 先做内容类型预检（Go 侧轻量 GET，不开浏览器）：
				// json/xml 等接口「发现即结果」直接记录，省下的 tab 预算让给真正的页面；
				// 是 HTML（SPA 路由等）才进 tab 队列。预检失败保守回退开 tab。
				if !extract.IsDocumentURL(uif.Url) {
					probe := req.ProbeContentType(uif.Url)
					if probe.Ok && probe.ContentType != "" && !req.IsHTMLContentType(probe.ContentType) {
						pu := &PendingUrl{
							URL:             uif.Url,
							Method:          "GET",
							Headers:         http.Header{},
							Status:          probe.StatusCode,
							ResponseHeaders: probe.Headers,
						}
						if u, err := urlpkg.Parse(uif.Url); err == nil {
							pu.Host = u.Host
						}
						pushpendingNormalizeQueue(pu)
						continue
					}
				}
				PushTabQueue(uif)
			}
		}
	}
}

func urlsQueueEmpty(ctx context.Context) {
	// 必须感知 ctx 取消：StopCurrent 后 PendUrlWork 协程已退出，
	// 没有消费者排空队列，这里的「等队列空」会变成永久死锁
	//（实测 web 控制台停止后任务状态永远 running）。
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if len(UrlsQueue) == 0 {
			break
		}
		time.Sleep(1 * time.Second)
	}
}
