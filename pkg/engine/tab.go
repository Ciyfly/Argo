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
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
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
			log.Logger.Errorf("page close error: %s ", e.Error())
		}
	}
	log.Logger.Debugf("TabLimit  2: %d", len(TabLimit))
	if timeoutFlage == NOT_PAGE_TIME_FLAG {
		tabDone <- true
	}
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

func (ei *EngineInfo) NewTab(uif *UrlInfo, pageFlag int) {
	// 测试不通直接放弃
	if !req.CheckTarget(uif.Url) {
		log.Logger.Debugf("CheckTarget: %s ", uif.Url)
		return
	}
	// tab关闭通道
	tabDone := make(chan bool, 1)
	var page *rod.Page
	var pageError error
	var NormalDoneFlag = false
	var TimeoutDoneFlag = false
	if TabLimitCloseFlag {
		return
	}
	var PushUrlWg sync.WaitGroup
	go func() {
		// 创建tab
		page, pageError = ei.Browser.Page(proto.TargetCreateTarget{URL: uif.Url})
		if pageError != nil || page == nil {
			tabDone <- true
			return
		}
		page.WaitLoad()
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
		// 延迟一会等待加载
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
			ei.NormalCloseTab(page, pageFlag, tabDone)
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
			ei.TimeoutCloseTab(page, pageFlag, tabDone)
		}
	}
}

// 接收所有静态url 来处理
var UrlsQueue chan *UrlInfo
var UrlsQueueCloseFlag bool
var TabQueue chan *UrlInfo

func (ei *EngineInfo) InitTabPool() {
	UrlsQueue = make(chan *UrlInfo, 10000)
	TabQueue = make(chan *UrlInfo, conf.GlobalConfig.BrowserConf.TabCount)
	TabLimit = make(chan int, conf.GlobalConfig.BrowserConf.TabCount)
	// for i := 1; i < conf.GlobalConfig.BrowserConf.TabCount; i++ {
	go ei.StaticUrlWork()
	// }
	go ei.TabWork()
}

func CloseUrlQueue() {
	UrlsQueueCloseFlag = true
	close(UrlsQueue)
}
func PushStaticUrl(uif *UrlInfo) {
	if UrlsQueueCloseFlag {
		return
	}
	UrlsQueue <- uif
}

func PushTabQueue(uif *UrlInfo) {
	log.Logger.Debugf("submit url: %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
	TabQueue <- uif
}

func (ei *EngineInfo) TabWork() {
	for {
		select {
		case TabLimit <- 1:
			// 从队列中获取一个 URL 对象并创建新协程去处理它
			uif, ok := <-TabQueue
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
		uif, ok := <-UrlsQueue
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
		if len(UrlsQueue) == 0 {
			break
		}
		time.Sleep(1 * time.Second)
	}
}
