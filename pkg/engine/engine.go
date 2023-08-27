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
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sync"
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
	FirstPageCloseChan chan bool
	Target             string
	Host               string
	HostName           string
	TabCount           int
	Page404PageURl     string
	Page404Vector      vector.Vector
	Page404Dict        map[string]int
}

type UrlInfo struct {
	Url        string
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
	// 初始化 泛化模块
	InitNormalize()
	// 初始化 结果处理模块
	InitResultHandler()
	// 初始化静态资源过滤
	InitFilter()
	// 初始化浏览器
	engineInfo := InitEngineInfo(target)
	// 初始化tab控制携程池
	engineInfo.InitTabPool()
	return engineInfo
}

func NewBrowserOptions() *launcher.Launcher {
	options := launcher.New().NoSandbox(true).Headless(true)
	// 指定chrome浏览器路径
	if conf.GlobalConfig.BrowserConf.Chrome != "" {
		log.Logger.Infof("chrome path: %s", conf.GlobalConfig.BrowserConf.Chrome)
		options.Bin(conf.GlobalConfig.BrowserConf.Chrome)
	}
	// 禁用所有提示防止阻塞 浏览器
	options = options.Append("disable-infobars", "")
	options = options.Append("disable-extensions", "")
	options.Set("disable-web-security")
	options.Set("allow-running-insecure-content")
	options.Set("reduce-security-for-testing")
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
	}
}

func (ei *EngineInfo) Start() {
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
			PushStaticUrl(&UrlInfo{Url: staticUrl, SourceType: "metadata parse", SourceUrl: "robots.txt|sitemap.xml", Depth: 0})
		}(staticUrl)
	}
	// 等待 metadata 爬取完成
	metadataWg.Wait()
	// 打开第一个tab页面 这里应该提交url管道任务
	// go ei.NewTab(&UrlInfo{Url: ei.Target, Depth: 0, SourceType: "homePage", SourceUrl: "target"}, HOME_PAGE_FLAG)
	PushStaticUrl(&UrlInfo{Url: ei.Target, Depth: 0, SourceType: "homePage", SourceUrl: "target"})
	page404url := ei.Target + "/" + utils.GenRandStr()
	ei.Page404PageURl = page404url
	// go ei.NewTab(&UrlInfo{Url: page404url, Depth: 0, SourceType: "404", SourceUrl: "404"}, RANDPAGE404_FLAG)
	PushStaticUrl(&UrlInfo{Url: page404url, Depth: 0, SourceType: "404", SourceUrl: "404"})
	// dev模式的时候不会结束 为了从浏览器界面调试查看需要手动关闭
	if conf.GlobalConfig.Dev {
		log.Logger.Warn("!!! dev mode please ctrl +c kill !!!")
		select {}
	}
	// 结束
	ei.Finish()
	ei.SaveResult()
}

func (ei *EngineInfo) AddBrowser(browser *rod.Browser, options *launcher.Launcher) {
	ei.BrowserList = append(ei.BrowserList, browser)
	ei.OptionsList = append(ei.OptionsList, options)
}
func (ei *EngineInfo) DelBrowser(delb *rod.Browser, delo *launcher.Launcher) {
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
		urlsQueueEmpty()
		log.Logger.Debug("------------------------urlsQueueEmpty over------------------------")
		// tab 的协程都完成了
		TabWg.Wait()
		log.Logger.Debug("------------------------tabPool over------------------------")
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
	CloseNormalizeQueue()
}

func (ei *EngineInfo) Close() {
	ei.SaveResult()
	// 关闭所有浏览器
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
