package engine

import (
	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/playback"
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
}

// tab 协程组
var TabWg sync.WaitGroup

// 控制tab的数量
var TabLimit chan int

// TabLimit 关闭flag
var TabLimitCloseFlag bool

func (ei *EngineInfo) closeTab(bi *BrowserInfo) {

	log.Logger.Errorf("browser list  1: %d", len(ei.BrowserList))
	if bi.Browser != nil {
		err := bi.Browser.Close()
		if err != nil {
			log.Logger.Errorf("browser close error: %s ", err.Error())
		}
		bi.Options.Kill()
	}
	ei.DelBrowser(bi.Browser, bi.Options)
	log.Logger.Errorf("browser list  2: %d", len(ei.BrowserList))
	if bi.TimeoutFlage == NOT_PAGE_TIME_FLAG {
		bi.TabDone <- true
	}
	if bi.PageFlag == HOME_PAGE_FLAG {
		ei.FirstPageCloseChan <- true
	}
}

func (ei *EngineInfo) NormalCloseTab(bi *BrowserInfo) {
	log.Logger.Errorf("NormalCloseTab %d", bi.PageFlag)
	bi.TimeoutFlage = NOT_PAGE_TIME_FLAG
	ei.closeTab(bi)
}
func (ei *EngineInfo) TimeoutCloseTab(bi *BrowserInfo) {
	log.Logger.Errorf("TimeoutCloseTab %d", bi.PageFlag)
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
		// 用于屏蔽某些请求 img、font
		// *.woff2 字体
		if ctx.Request.Type() == proto.NetworkResourceTypeFont {
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		// 图片
		if ctx.Request.Type() == proto.NetworkResourceTypeImage {
			ctx.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}

		// 防止空指针
		if ctx.Request.Req() != nil && ctx.Request.Req().URL != nil {

			// 优化, 先判断,再组合
			if strings.Contains(ctx.Request.URL().String(), ei.HostName) {
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
				if ctx.Request.URL().String() == ei.Page404PageURl {
					// 随机请求的url 404
					return
				}
				// fix 管道关闭了但是还推数据的问题
				if NormalizeCloseChanFlag {
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
					pushpendingNormalizeQueue(pu)
				}
			}
		}
		ctx.ContinueRequest(&proto.FetchContinueRequest{})

	})
	go router.Run()

	// tab相关
	tabDone := make(chan bool, 1)
	var page *rod.Page
	var pageError error
	var NormalDoneFlag = false
	var TimeoutDoneFlag = false
	if TabLimitCloseFlag {
		return
	}
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
		page, pageError = browser.Page(proto.TargetCreateTarget{URL: uif.Url})
		if pageError != nil {
			page.Reload()
		}
		info, err := utils.GetPageInfoByPage(page)
		if err != nil {
			log.Logger.Errorf("GetPageInfoByPage: %s", err.Error())
			ei.NormalCloseTab(browserInfo)
			return
		}
		// 404 页面判断
		if pageFlag == RANDPAGE404_FLAG {
			html, _ := page.HTML()
			ei.Page404Vector = vector.HTMLToVector(html)
			ei.NormalCloseTab(browserInfo)
			return
		}
		html, _ := page.HTML()
		if strings.Contains(info.Title, "404") || static.Match404ResponsePage([]byte(html)) {
			browserInfo.PageFlag = PAGE404_FLAG
			ei.NormalCloseTab(browserInfo)
			return
		}
		// 调试模式 手动去操作 停止所有
		if conf.GlobalConfig.Dev {
			// ei.NormalCloseTab(page, pageFlag)
			return
		}

		// 判断页面是不是404页面
		currentPageVector := vector.HTMLToVector(html)
		similarity := vector.CosineSimilarity(ei.Page404Vector, currentPageVector)
		log.Logger.Debugf("similarity: %f", similarity)
		if similarity > 0.95 {
			ei.Page404Dict[uif.Url] = 1
			log.Logger.Debugf("similarity: %f", similarity)
			log.Logger.Debugf("404 page: %s", uif.Url)
			log.Logger.Info("similarity")
			ei.NormalCloseTab(browserInfo)
			return
		}
		if pageFlag == HOME_PAGE_FLAG {
			//  执行headless脚本 只有访问第一个页面的时候才会执行
			if conf.GlobalConfig.PlaybackPath != "" {
				log.Logger.Debugf("run playback script: %s", conf.GlobalConfig.PlaybackPath)
				playback.Run(conf.GlobalConfig.PlaybackPath, page)
			}
		}
		if conf.GlobalConfig.TestPlayBack {
			time.Sleep(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second)
			ei.NormalCloseTab(browserInfo)
			return
		}
		log.Logger.Debugf("[ new tab  ]=> %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
		// 注入js dom构建前
		inject.InjectScript(page, 0)
		// 延迟一会等待加载
		page.WaitLoad()
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
				go func(staticUrl string) {
					defer PushUrlWg.Done()
					PushStaticUrl(&UrlInfo{Url: staticUrl, SourceType: "static parse", SourceUrl: uif.Url, Depth: uif.Depth + 1})
				}(staticUrl)
			}
		}
		// 执行自动化触发事件 输入 点击等 auto
		hrefList := inject.Auto(page)
		// auto 触发后 获取下当前url
		info, err = utils.GetPageInfoByPage(page)
		var currentUrl = ""
		if err != nil {
			log.Logger.Debugf("page timeout:%s  %s", err, uif.Url)
		} else {
			currentUrl = info.URL
		}
		log.Logger.Debugf("dynamic %s parse count: %d", uif.Url, len(staticUrlList))
		// 解析demo
		for _, staticUrl := range hrefList {
			PushUrlWg.Add(1)
			go func(staticUrl string) {
				defer PushUrlWg.Done()
				PushStaticUrl(&UrlInfo{Url: staticUrl, SourceType: "auto js", SourceUrl: uif.Url, Depth: uif.Depth + 1})
			}(staticUrl)
		}

		// 推送下如果 单纯的去修改当前页面url的形式
		// https://spa5.scrape.center/page/1
		if currentUrl != "" {
			PushUrlWg.Add(1)
			go func(currentUrl string) {
				defer PushUrlWg.Done()
				PushStaticUrl(&UrlInfo{Url: info.URL, SourceType: "patch", SourceUrl: uif.Url, Depth: uif.Depth + 1})
			}(currentUrl)
		}
		// 所有url提交完成才能结束
		PushUrlWg.Wait()
		NormalDoneFlag = true
		if !TimeoutDoneFlag {
			ei.NormalCloseTab(browserInfo)
		}

	}() // 协程
	// 阻塞超时控制
	select {
	case <-tabDone:
		log.Logger.Debugf("[close tab ] => %s", uif.Url)
	case <-time.After(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second):
		log.Logger.Warnf("[timeout tab ] => %s", uif.Url)
		if !NormalDoneFlag {
			TimeoutDoneFlag = true
			ei.TimeoutCloseTab(browserInfo)
		}
	}
}

// 接收所有静态url 来处理
var urlsQueue chan *UrlInfo
var tabQueue chan *UrlInfo

func (ei *EngineInfo) InitTabPool() {
	urlsQueue = make(chan *UrlInfo, 10000000)
	tabQueue = make(chan *UrlInfo, conf.GlobalConfig.BrowserConf.TabCount)
	TabLimit = make(chan int, conf.GlobalConfig.BrowserConf.TabCount)
	go ei.StaticUrlWork()
	go ei.TabWork()
}

func PushStaticUrl(uif *UrlInfo) {
	urlsQueue <- uif
}

func PushTabQueue(uif *UrlInfo) {
	log.Logger.Debugf("submit url: %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
	tabQueue <- uif
}

func (ei *EngineInfo) TabWork() {
	for {
		select {
		case TabLimit <- 1:
			// 从队列中获取一个 URL 对象并创建新协程去处理它
			uif := <-tabQueue
			// 不包含根url的直接不进行访问
			if !strings.Contains(uif.Url, ei.Host) {
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
					<-TabLimit
					TabWg.Done()

				}()
				log.Logger.Debugf("[ new tab  ]=> %s", uif.Url)
				if uif.SourceType == "homePage" {
					ei.NewTab(uif, HOME_PAGE_FLAG)
				} else {
					ei.NewTab(uif, NOT_HOME_PAGE_FLAG)
				}
			}()
		default:
			log.Logger.Debug("wait sleep 1s")
			time.Sleep(1 * time.Second)
			continue
		}
	}
}

func (ei *EngineInfo) StaticUrlWork() {
	for {
		uif := <-urlsQueue
		if uif.Url == "" {
			continue
		}
		// pass 掉host之外的域名
		if strings.Contains(uif.Url, "http") && !strings.Contains(uif.Url, ei.Host) {
			continue
		}
		if filterStatic(uif.Url) {
			// 静态资源不处理
			continue
		} // 泛化后不重复才会请求
		if !urlIsExists(uif.Url) {
			PushTabQueue(uif)
		}
	}
}

func urlsQueueEmpty() {
	for {
		if len(urlsQueue) == 0 {
			break
		}
		time.Sleep(1 * time.Second)
	}
}
