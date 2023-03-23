package engine

import (
	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/playback"
	"argo/pkg/static"
	"argo/pkg/utils"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type Faker struct {
}

func (f *Faker) Write(p []byte) (n int, err error) {
	return 0, nil
}

var TabWg sync.WaitGroup
var TabLimit chan int

func (ei *EngineInfo) closeTab(page *rod.Page, flag int, done chan bool) {
	if flag == 0 {
		ei.CloseChan <- flag
	}
	<-TabLimit
	if page != nil {
		page.Close()
	}
	TabWg.Done()
}

func (ei *EngineInfo) NewTab(uif *UrlInfo, flag int) {
	TabLimit <- 1
	var PushUrlWg sync.WaitGroup
	done := make(chan bool)
	ei.TabCount += 1
	go func() {
		// 创建tab
		page, err := ei.Browser.Page(proto.TargetCreateTarget{URL: uif.Url})
		// log.Logger.Debug(page.HTML())
		info, err := utils.GetPageInfoByPage(page)
		if err != nil {
			// 超时干掉了page
			ei.closeTab(page, flag, done)
			return
		}
		html, _ := page.HTML()
		if strings.Contains(info.Title, "404") || static.Match404ResponsePage([]byte(html)) {
			ei.closeTab(page, flag, done)
			return
		}
		// 调试模式 手动去操作 停止所有
		if conf.GlobalConfig.Dev {
			ei.closeTab(page, flag, done)
			return
		}
		if err != nil {
			log.Logger.Errorf("page %s error: %s  sourceType: %s sourceUrl: %s", uif.Url, err, uif.SourceType, uif.SourceUrl)
			ei.closeTab(page, flag, done)
			return
		}
		if flag == 0 {
			//  执行headless脚本 只有访问第一个页面的时候才会执行
			if conf.GlobalConfig.PlaybackPath != "" {
				log.Logger.Debugf("run playback script: %s", conf.GlobalConfig.PlaybackPath)
				playback.Run(conf.GlobalConfig.PlaybackPath, page)
			}
		}
		if conf.GlobalConfig.TestPlayBack {
			time.Sleep(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second)
			ei.closeTab(page, flag, done)
			return
		}
		// 设置超时时间
		page.Timeout(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second)
		log.Logger.Debugf("[ new tab  ]=> %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
		// 注入js dom构建前
		inject.InjectScript(page, 0)
		// 延迟一会等待加载
		// time.Sleep(3 * time.Second)
		page.WaitLoad()
		// 判断是否需要登录 需要的话进行自动化尝试登录
		login.GlobalLoginAutoData.Handler(page)
		// 注入js dom构建后
		inject.InjectScript(page, 1)
		// 静态解析下dom 爬取一些url
		staticUrlList := static.ParseDom(page)
		// log.Logger.Debugf("parse %s -> staticUrlList: %s", uif.Url, staticUrlList)
		if staticUrlList != nil {
			for _, staticUrl := range staticUrlList {
				PushUrlWg.Add(1)
				go func(staticUrl string) {
					defer PushUrlWg.Done()
					PushStaticUrl(&UrlInfo{Url: staticUrl, SourceType: "static parse", SourceUrl: uif.Url})
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
		// 解析demo
		for _, staticUrl := range hrefList {
			PushUrlWg.Add(1)
			go func(staticUrl string) {
				defer PushUrlWg.Done()
				PushStaticUrl(&UrlInfo{Url: staticUrl, SourceType: "auto js", SourceUrl: uif.Url})
			}(staticUrl)
		}
		// 推送下如果 单纯的去修改当前页面url的形式
		// https://spa5.scrape.center/page/1
		if currentUrl != "" {
			PushUrlWg.Add(1)
			go func(currentUrl string) {
				defer PushUrlWg.Done()
				PushStaticUrl(&UrlInfo{Url: info.URL, SourceType: "patch", SourceUrl: uif.Url})
			}(currentUrl)
		}
		// 所有url提交完成才能结束
		PushUrlWg.Wait()
		ei.closeTab(page, flag, done)

	}() // 协程
	// 阻塞超时控制
	select {
	case <-done:
		log.Logger.Debugf("[close tab ] => %s", uif.Url)
	case <-time.After(time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second):
		log.Logger.Warnf("[timeout tab ] => %s", uif.Url)
		ei.closeTab(nil, flag, done)
	}
}

// 接收所有静态url 来处理
var urlsQueue chan *UrlInfo
var tabQueue chan *UrlInfo

func (ei *EngineInfo) InitTabPool() {
	urlsQueue = make(chan *UrlInfo, 10000)
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
		uif := <-tabQueue
		TabWg.Add(1)
		go ei.NewTab(uif, 1)
	}
}

func (ei *EngineInfo) StaticUrlWork() {
	for {
		uif := <-urlsQueue
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
