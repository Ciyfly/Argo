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
}

func (ei *EngineInfo) closeTab(bi *BrowserInfo) {

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
	var page *rod.Page
	var pageError error
	var NormalDoneFlag = false
	var TimeoutDoneFlag = false
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
		page.WaitLoad()
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
		if conf.GlobalConfig.TestPlayBack {
			time.Sleep(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second)
			ei.NormalCloseTab(browserInfo)
			return
		}
		log.Logger.Debugf("[ new tab  ]=> %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
		// 注入js dom构建前/后
		inject.InjectScript(page, 0)
		inject.InjectScript(page, 1)
		ctx := &PageContext{
			Engine:   ei,
			Page:     page,
			Url:      uif,
			PageFlag: pageFlag,
		}
		ei.runPageMiddlewares(ctx)
		// auto 触发后 获取下当前url
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
			ei.NormalCloseTab(browserInfo)
		}
		// 推送下如果 单纯的去修改当前页面url的形式
		// https://spa5.scrape.center/page/1
		if currentUrl != "" {
			PushUrlWg.Add(1)
			go func(currentUrl string) {
				defer PushUrlWg.Done()
				ei.PushStaticUrl(&UrlInfo{Url: info.URL, SourceType: "patch", SourceUrl: uif.Url, Depth: uif.Depth + 1})
			}(currentUrl)
		}
		// 所有url提交完成才能结束
		PushUrlWg.Wait()

	}() // 协程
	// 阻塞超时控制
	select {
	case <-tabDone:
		log.Logger.Debugf("[close tab ] => %s", uif.Url)
	case <-time.After(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second):
		log.Logger.Warnf("[timeout tab ] => %s", uif.Url)
		if !NormalDoneFlag {
			atomic.AddInt64(&ei.TabsTimeout, 1)
			ei.EmitEvent(EngineEvent{Type: "tab_timeout", Target: uif.Url, Timestamp: time.Now()})
			TimeoutDoneFlag = true
			ei.TimeoutCloseTab(browserInfo)
		}
	}
}
