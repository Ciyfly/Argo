package engine

import (
	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/playback"
	"argo/pkg/static"
	"argo/pkg/utils"
	"argo/pkg/vector"
	"context"
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
			log.Logger.Debug("page close error: %s ", e.Error())
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

func (ei *EngineInfo) waitForPageLoad(page *rod.Page) bool {
	loadedChan := make(chan bool, 1)

	go func() {
		// 等待页面加载事件
		page.WaitLoad()

		// 等待网络请求变为空闲
		page.WaitRequestIdle(2*time.Second, nil, nil, nil)()

		// 检查 DOM 是否稳定
		page.WaitDOMStable(2*time.Second, 0.1)

		// 执行自定义 JavaScript 来检查页面状态
		js := `() => {
            return {
                readyState: document.readyState,
                loadingComplete: !document.querySelector('body[unresolved]'),
                noLoadingIndicators: !document.querySelector('.loading, #loading, .spinner, #spinner'),
                allImagesLoaded: Array.from(document.images).every((img) => img.complete)
            }
        }`

		for i := 0; i < 5; i++ { // 尝试最多5次
			result, err := page.Eval(js)
			if err == nil {
				status := result.Value.Map()
				if status["readyState"].Str() == "complete" &&
					status["loadingComplete"].Bool() &&
					status["noLoadingIndicators"].Bool() &&
					status["allImagesLoaded"].Bool() {
					loadedChan <- true
					return
				}
			}
			time.Sleep(1 * time.Second)
		}

		// 如果所有检查都通过但仍未满足条件，我们假设页面已经加载完毕
		loadedChan <- true
	}()

	// 设置一个最大等待时间
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
	var PushUrlWg sync.WaitGroup
	tabPool, _ := ants.NewPoolWithFunc(100, func(data interface{}) {
		urlInfo := data.(*UrlInfo)
		PushUrlQueue(urlInfo)
		PushUrlWg.Done()
	})
	defer tabPool.Release()

	domLoadedChan := make(chan bool, 1)

	go func() {
		// 创建tab
		page, pageError = ei.Browser.Page(proto.TargetCreateTarget{URL: uif.Url})
		if pageError != nil || page == nil {
			tabDone <- true
			return
		}

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
		ei.TabCount += 1
		// 404 页面判断
		if pageFlag == RANDPAGE404_FLAG {
			html, _ := page.HTML()
			ei.Page404Vector = vector.HTMLToVector(html)
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
		similarity := vector.CosineSimilarity(ei.Page404Vector, currentPageVector)
		log.Logger.Debugf("similarity: %f", similarity)
		if similarity > 0.95 {
			ei.Page404Dict[uif.Url] = 1
			log.Logger.Debugf("similarity: %f", similarity)
			log.Logger.Debugf("404 page: %s", uif.Url)
			log.Logger.Info("similarity")
			ei.NormalCloseTab(page, pageFlag, tabDone)
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
			ei.NormalCloseTab(page, pageFlag, tabDone)
			return
		}
		log.Logger.Debugf("[ new tab  ]=> %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
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
				_ = tabPool.Invoke(data)
			}
		}
		// 执行自动化触发事件 输入 点击等 auto
		hrefList := inject.Auto(page)
		// auto 触发后 获取下当前url
		info, err = utils.GetPageInfoByPage(page)
		var currentUrl = ""
		if err != nil {
			log.Logger.Debugf("page timeout:%s  %s", err, uif.Url)
			tabDone <- true
			return
		} else {
			currentUrl = info.URL
		}
		log.Logger.Debugf("dynamic %s parse count: %d", uif.Url, len(staticUrlList))
		// 解析demo
		for _, staticUrl := range hrefList {
			PushUrlWg.Add(1)
			data := &UrlInfo{Url: staticUrl, SourceType: "auto js", SourceUrl: uif.Url, Depth: uif.Depth + 1}
			_ = tabPool.Invoke(data)
		}

		// 推送下如果 单纯的去修改当前页面url的形式
		if currentUrl != "" {
			PushUrlWg.Add(1)
			data := &UrlInfo{Url: info.URL, SourceType: "patch", SourceUrl: uif.Url, Depth: uif.Depth + 1}
			_ = tabPool.Invoke(data)
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

	// DOM加载完成后，使用配置的TabTimeout进行后续操作的超时控制
	select {
	case <-tabDone:
		log.Logger.Debugf("[close tab ] => %s", uif.Url)
		NormalDoneFlag = true
		if !TimeoutDoneFlag {
			ei.NormalCloseTab(page, pageFlag, tabDone)
		}
	case <-time.After(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second):
		log.Logger.Warnf("[timeout tab ] => %s", uif.Url)
		if !NormalDoneFlag {
			TimeoutDoneFlag = true
			ei.TimeoutCloseTab(page, pageFlag, tabDone)
		}
	}
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
						TabWg.Done()
						<-TabLimit
					}()
					log.Logger.Debugf("[ new tab  ]=> %s", uif.Url)
					if uif.SourceType == "homePage" {
						ei.NewTab(uif, HOME_PAGE_FLAG)
					} else {
						ei.NewTab(uif, NOT_HOME_PAGE_FLAG)
					}
				}()
			}
		default:
			log.Logger.Debug("wait sleep 1s")
			time.Sleep(1 * time.Second)
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
			if uif.Url == "" {
				continue
			}
			// pass 掉host之外的域名
			if strings.Contains(uif.Url, "http") && !strings.Contains(uif.Url, ei.Host) {
				continue
			}
			if filterStaticPendUrl(uif.Url) {
				// 静态资源不会去打开js
				continue
			} // 泛化后不重复才会请求

			if !urlIsExists(uif.Url) {
				PushTabQueue(uif)
			}
		}
	}
}

func urlsQueueEmpty() {
	for {
		if len(UrlsQueue) == 0 {
			break
		}
		time.Sleep(1 * time.Second)
	}
}
