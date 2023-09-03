package engine

import (
	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/req"
	"argo/pkg/static"
	"argo/pkg/utils"
	"argo/pkg/vector"
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
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
	Browser            *rod.Browser
	Options            conf.BrowserConf
	Launcher           *launcher.Launcher
	FirstPageCloseChan chan bool
	MonitorChan        chan bool
	Target             string
	Host               string
	HostName           string
	TabCount           int
	Page404PageURl     string
	Page404Vector      vector.Vector
	Page404Dict        map[string]int
	Ctx                context.Context
	Cancel             context.CancelFunc
}

type UrlInfo struct {
	Url        string
	SourceType string
	Match      string
	SourceUrl  string
	Depth      int
}

func Run(target string) {
	ctx, cancel := context.WithCancel(context.Background())
	eif := InitEngine(ctx, target)
	if eif != nil {
		eif.Start(ctx)
	} else {
		cancel()
		return
	}
	cancel()
	ClearChan()
}
func InitEngine(ctx context.Context, target string) *EngineInfo {
	// 初始化 js注入插件
	inject.LoadScript()
	// 初始化 登录插件
	login.InitLoginAuto()
	// 初始化 泛化模块
	InitNormalize(ctx)
	// 初始化 结果处理模块
	InitResultHandler(ctx)
	// 初始化静态资源过滤
	InitFilter()
	// 初始化浏览器
	engineInfo := InitBrowser(target)
	// 初始化tab控制携程池
	engineInfo.InitTabPool(ctx)
	return engineInfo
}

func InitBrowser(target string) *EngineInfo {
	// 初始化
	browser := rod.New()
	// 启动无痕
	if conf.GlobalConfig.BrowserConf.Trace {
		browser = browser.Trace(true)
	}
	// options := launcher.New().Devtools(true)
	//  NoSandbox fix linux下root运行报错的问题
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
		browser = browser.SlowMotion(time.Duration(conf.GlobalConfig.AutoConf.Slow) * time.Second)
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
	if conf.GlobalConfig.BrowserConf.Remote != "" {
		log.Logger.Infof("chrome remote: %s", conf.GlobalConfig.BrowserConf.Remote)
		browser = browser.ControlURL(conf.GlobalConfig.BrowserConf.Remote)
	} else {
		browser = browser.ControlURL(options.MustLaunch())
	}
	err := browser.Connect()
	if err != nil {
		log.Logger.Errorf("browser connect err:%s ", err)
		return nil
	}
	browser.NoDefaultDevice().MustIncognito()
	browser.MustIgnoreCertErrors(true)
	firstPageCloseChan := make(chan bool, 1)
	monitorChan := make(chan bool, 1)
	u, _ := url.Parse(target)

	return &EngineInfo{
		Browser:            browser,
		Options:            conf.GlobalConfig.BrowserConf,
		Launcher:           options,
		FirstPageCloseChan: firstPageCloseChan,
		MonitorChan:        monitorChan,
		Target:             target,
		Host:               u.Host,
		HostName:           u.Hostname(),
		Page404Dict:        make(map[string]int),
	}
}

func (ei *EngineInfo) CloseBrowser() {
	ei.MonitorChan <- true
	if ei.Browser != nil {
		closeErr := ei.Browser.Close()
		if closeErr != nil {
			log.Logger.Errorf("browser close err: %s", closeErr)
		} else {
			log.Logger.Debug("browser close over")
		}
	}

}

func (ei *EngineInfo) Finish() {
	// 1. 任务完成 2. 浏览器超时
	tabOverChan := make(chan bool, 1)
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
		tabOverChan <- true
	}()
	select {
	case <-tabOverChan:
		log.Logger.Debug("------------------------task over------------------------")
	// 浏览器超时
	case <-time.After(time.Duration(conf.GlobalConfig.BrowserConf.BrowserTimeout) * time.Second):
		log.Logger.Warnf("------------------------browser timeout, close browser %ds", conf.GlobalConfig.BrowserConf.BrowserTimeout)
	}
	ei.CloseBrowser()
}

func (ei *EngineInfo) Start(ctx context.Context) {
	var reqClient *http.Client
	if conf.GlobalConfig.BrowserConf.Proxy != "" {
		log.Logger.Debugf("proxy: %s", conf.GlobalConfig.BrowserConf.Proxy)
	}
	log.Logger.Debugf("tab timeout: %ds", conf.GlobalConfig.BrowserConf.TabTimeout)
	log.Logger.Debugf("browser timeout: %ds", conf.GlobalConfig.BrowserConf.BrowserTimeout)
	log.Logger.Debugf("tab controller count: %d", conf.GlobalConfig.BrowserConf.TabCount)
	// hook 请求响应获取所有异步请求
	router := ei.Browser.HijackRequests()
	defer router.Stop()
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
	// 这个是 robots.txt|sitemap.xml 爬取解析的
	var metadataWg sync.WaitGroup
	metadataList := static.MetaDataSpider(ei.Target)
	for _, staticUrl := range metadataList {
		metadataWg.Add(1)
		log.Logger.Debugf("metadata parse: %s", staticUrl)
		go func(staticUrl string) {
			defer metadataWg.Done()
			PushUrlQueue(&UrlInfo{Url: staticUrl, SourceType: "metadata parse", SourceUrl: "robots.txt|sitemap.xml", Depth: 0})
		}(staticUrl)
	}
	// 等待 metadata 爬取完成
	metadataWg.Wait()
	// 打开第一个tab页面 这里应该提交url管道任务
	PushUrlQueue(&UrlInfo{Url: ei.Target, Depth: 0, SourceType: "homePage", SourceUrl: "target"})
	page404url := ei.Target + "/" + utils.GenRandStr()
	ei.Page404PageURl = page404url
	// go ei.NewTab(&UrlInfo{Url: page404url, Depth: 0, SourceType: "404", SourceUrl: "404"}, RANDPAGE404_FLAG)
	PushUrlQueue(&UrlInfo{Url: page404url, Depth: 0, SourceType: "404", SourceUrl: "404"})
	// 定时清空 about:blank#blocked 当浏览器也退出
	go func() {
		for {
			select {
			case <-ei.MonitorChan:
				return
			default:
				pages, err := ei.Browser.Pages()
				if err != nil {
					log.Logger.Debugf("monitor get pages: %s", err)
					time.Sleep(3 * time.Second)
					continue
				}
				for _, p := range pages {
					info, _ := utils.GetPageInfoByPage(p)
					if info != nil {
						if info.URL == "about:blank#blocked" {
							log.Logger.Debugf("!!!!!!!!! del about")
							p.Close()
						}
					}

				}
				time.Sleep(3 * time.Second)
			}
		}

	}()
	// dev模式的时候不会结束 为了从浏览器界面调试查看需要手动关闭
	if conf.GlobalConfig.Dev {
		log.Logger.Warn("!!! dev mode please ctrl +c kill !!!")
		select {}
	}
	// 结束
	ei.Finish()
	ei.Launcher.Kill()
	ei.SaveResult()
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

func ClearChan() {
	for {
		select {
		case <-UrlsQueue:
		case <-TabQueue:
		case <-TabLimit:
		case <-PendingNormalizeQueue:
		default:
			return
		}
	}
}
