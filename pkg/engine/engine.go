package engine

import (
	"argo/pkg/conf"
	"argo/pkg/extract"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/ratelimit"
	"argo/pkg/req"
	"argo/pkg/scope"
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

	// 以下字段会被多个 tab 协程和流量劫持回调协程同时访问。
	// fix: 之前没有任何同步，并发读写 map 会直接 panic（fatal error: concurrent map read and map write），
	// TabCount 的 += 也不是原子操作。统一用这把锁保护。
	mu sync.RWMutex
}

// IncrTabCount 增加已打开 tab 计数。
func (ei *EngineInfo) IncrTabCount() {
	ei.mu.Lock()
	ei.TabCount++
	ei.mu.Unlock()
}

// GetTabCount 读取已打开 tab 计数。
func (ei *EngineInfo) GetTabCount() int {
	ei.mu.RLock()
	defer ei.mu.RUnlock()
	return ei.TabCount
}

// SetPage404Vector 记录随机 404 页面的向量特征。
func (ei *EngineInfo) SetPage404Vector(v vector.Vector) {
	ei.mu.Lock()
	ei.Page404Vector = v
	ei.mu.Unlock()
}

// GetPage404Vector 读取随机 404 页面的向量特征。
func (ei *EngineInfo) GetPage404Vector() vector.Vector {
	ei.mu.RLock()
	defer ei.mu.RUnlock()
	return ei.Page404Vector
}

// SetPage404PageURL 记录随机 404 页面的 URL。
func (ei *EngineInfo) SetPage404PageURL(u string) {
	ei.mu.Lock()
	ei.Page404PageURl = u
	ei.mu.Unlock()
}

// GetPage404PageURL 读取随机 404 页面的 URL。
func (ei *EngineInfo) GetPage404PageURL() string {
	ei.mu.RLock()
	defer ei.mu.RUnlock()
	return ei.Page404PageURl
}

// MarkPage404 标记某个 URL 已判定为 404 页面。
func (ei *EngineInfo) MarkPage404(u string) {
	ei.mu.Lock()
	ei.Page404Dict[u] = 1
	ei.mu.Unlock()
}

// IsPage404 判断某个 URL 是否已被判定为 404 页面。
func (ei *EngineInfo) IsPage404(u string) bool {
	ei.mu.RLock()
	defer ei.mu.RUnlock()
	_, ok := ei.Page404Dict[u]
	return ok
}

type UrlInfo struct {
	Url        string
	SourceType string
	Match      string
	SourceUrl  string
	Depth      int
}

// OnTabOpen 在每个新 tab 页面创建成功后被调用（web 控制台用于跟随画面）。
// 可为 nil。
var OnTabOpen func(page *rod.Page)

// 当前运行任务的句柄（单任务并发），供 web 控制台停止
var (
	currentMu     sync.Mutex
	currentCancel context.CancelFunc
	currentEngine *EngineInfo
)

// StopCurrent 停止当前任务：cancel ctx + 关浏览器（触发 Finish 收尾）。
// 没有运行中的任务时是空操作。
func StopCurrent() {
	currentMu.Lock()
	cancel := currentCancel
	eif := currentEngine
	currentMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if eif != nil {
		eif.CloseBrowser()
	}
}

// RunAsync 启动一次爬取并立即返回，done 在爬取结束后关闭。
// target 为空或浏览器初始化失败时 done 立即关闭。
func RunAsync(target string) (done <-chan struct{}) {
	end := make(chan struct{})
	go func() {
		defer close(end)
		Run(target)
	}()
	return end
}

func Run(target string) {
	// 断点续爬：记录当前目标；状态加载必须在 InitEngine 之后（UrlsQueue 那时才创建）
	lastTarget = target
	ctx, cancel := context.WithCancel(context.Background())
	eif := InitEngine(ctx, target)
	if eif == nil {
		cancel()
		return
	}
	currentMu.Lock()
	currentCancel = cancel
	currentEngine = eif
	currentMu.Unlock()
	defer func() {
		currentMu.Lock()
		currentCancel = nil
		currentEngine = nil
		currentMu.Unlock()
	}()
	if conf.GlobalConfig.ResumePath != "" {
		if err := LoadCrawlState(conf.GlobalConfig.ResumePath, target); err != nil {
			log.Logger.Fatalf("load crawl state %s err: %s", conf.GlobalConfig.ResumePath, err)
		}
		// 首页上次已完成时不会被重新打开，Finish 会一直等 FirstPageCloseChan——
		// 预置完成信号让结束流程正常推进
		if urlIsExists(target) {
			log.Logger.Info("[  resume  ] 首页上次已完成，跳过首页等待")
			eif.FirstPageCloseChan <- true
		}
	}
	eif.Start(ctx)
	cancel()
	ClearChan()
}
func InitEngine(ctx context.Context, target string) *EngineInfo {
	// 初始化 scope 规则（host 精确匹配 + 可选正则），并清空上一轮域外记录
	scope.InitFromTarget(target, conf.GlobalConfig.ScopeConf)
	scope.ResetOutScope()
	// 表单填充规则正则启动即校验，写错第一时间 Fatal
	if _, err := inject.FormRulesCheck(); err != nil {
		log.Logger.Fatalf("form fill rules: %s", err)
	}
	// 初始化密钥规则表（默认规则 + config.yml 追加规则），非法正则直接终止
	if conf.GlobalConfig.ExtractConf.Secrets {
		customRules := make([]extract.SecretRule, 0, len(conf.GlobalConfig.ExtractConf.SecretRules))
		for _, rule := range conf.GlobalConfig.ExtractConf.SecretRules {
			customRules = append(customRules, extract.SecretRule{Name: rule.Name, Regex: rule.Regex, Severity: rule.Severity})
		}
		if err := extract.InitSecretRules(customRules); err != nil {
			log.Logger.Fatalf("init secret rules: %s", err)
		}
	}
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

// parseCookies 把命令行传入的 Cookie 字符串解析成 CDP 需要的结构。
//
// 支持两种格式：
//
//	name=value               —— 绑定到目标站点的域名
//	name=value@domain        —— 显式指定域名（如跨域 SSO 场景）
//
// 之所以用浏览器层注入（StorageSetCookies）而不是在每个请求头里拼：
// 注入一次后，页面导航、XHR、表单提交全部自动携带，与会话行为完全一致。
func parseCookies(raw []string, targetURL string) []*proto.NetworkCookieParam {
	if len(raw) == 0 {
		return nil
	}
	u, err := url.Parse(targetURL)
	if err != nil {
		if log.Logger != nil {
			log.Logger.Errorf("parse cookies: target url err: %s", err)
		}
		return nil
	}
	var out []*proto.NetworkCookieParam
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		eq := strings.Index(item, "=")
		if eq <= 0 {
			if log.Logger != nil {
				log.Logger.Errorf("cookie format err (want name=value): %s", item)
			}
			continue
		}
		name := item[:eq]
		value := item[eq+1:]
		domain := u.Hostname()
		// name=value@domain：显式指定域名
		if at := strings.LastIndex(value, "@"); at >= 0 {
			domain = value[at+1:]
			value = value[:at]
		}
		out = append(out, &proto.NetworkCookieParam{
			Name:   name,
			Value:  value,
			Domain: domain,
			Path:   "/",
			URL:    u.Scheme + "://" + u.Host,
		})
		// Logger 可能未初始化（如单元测试直调本函数），防御式判断
		if log.Logger != nil {
			log.Logger.Infof("preset cookie: %s (domain=%s)", name, domain)
		}
	}
	return out
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
	// 人工登录模式强制有头：验证码/短信/扫码要真人操作，浏览器必须可见
	forceHeaded := conf.GlobalConfig.BrowserConf.WaitLogin
	if conf.GlobalConfig.BrowserConf.UnHeadless || conf.GlobalConfig.Dev || forceHeaded {
		options = options.Delete("--headless")
		browser = browser.SlowMotion(time.Duration(conf.GlobalConfig.AutoConf.Slow) * time.Second)
	}
	// 扫描器推送优先于普通代理：浏览器只能设一个代理，
	// 上游代理（--proxy）应在扫描器侧配置（xray 支持上游代理）
	proxyAddr := conf.GlobalConfig.BrowserConf.PushProxy
	if proxyAddr == "" {
		proxyAddr = conf.GlobalConfig.BrowserConf.Proxy
	} else if conf.GlobalConfig.BrowserConf.Proxy != "" {
		log.Logger.Warnf("--proxy %s 被忽略：--pushproxy 生效，上游代理请在扫描器侧配置", conf.GlobalConfig.BrowserConf.Proxy)
	}
	if proxyAddr != "" {
		proxyURL, err := url.Parse(proxyAddr)
		if err != nil {
			log.Logger.Fatal("proxy err:", err)
		}
		options.Proxy(proxyURL.String())
	}
	// windows下 使用单进程 防止多个cmd窗口弹出
	if runtime.GOOS == "windows" {
		options = options.Set("single-process")
	}
	// 如果指定了ua 设置ua
	if conf.GlobalConfig.BrowserConf.UserAgent != "" {
		options.Set("user-agent", conf.GlobalConfig.BrowserConf.UserAgent)
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
	// 扫描器推送提示：pushproxy 不可达时浏览器所有请求都会失败，尽早警告
	if push := conf.GlobalConfig.BrowserConf.PushProxy; push != "" {
		log.Logger.Warnf("[   push   ] 全部流量经被动扫描器转发: %s（请确认扫描器已监听）", push)
	}
	browser.NoDefaultDevice().MustIncognito()
	browser.MustIgnoreCertErrors(true)
	// 注入预置会话 Cookie（--cookie），让爬虫能进入登录后才能访问的区域
	if cookies := parseCookies(conf.GlobalConfig.Cookies, target); len(cookies) > 0 {
		if err := browser.SetCookies(cookies); err != nil {
			log.Logger.Errorf("set cookies err: %s", err)
		}
	}
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

func (ei *EngineInfo) Finish(ctx context.Context) {
	// 1. 任务完成 2. 浏览器超时
	tabOverChan := make(chan bool, 1)
	go func() {
		// 任务完成
		// 当第一个页面访问完成后才会关闭
		<-ei.FirstPageCloseChan
		log.Logger.Debug("------------------------first page over------------------------")
		// url队列为空 没有新增的url需要测试了
		urlsQueueEmpty(ctx)
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
	// 定期保存断点状态（浏览器被强杀时最多丢 10s 进度）
	startStateSaver(ctx)
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
			if scope.IsInScope(ctx.Request.URL().String()) {
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
				// 这个回调会被多个请求并发调用，reqClient 必须是局部变量，
				// 否则并发写同一个变量会产生数据竞争。
				var reqClient *http.Client
				// proxy（pushproxy 优先，见 req.EffectiveProxy——扫描器推送时探测流量也不能绕过）
				if req.EffectiveProxy() != "" {
					reqClient = req.GetProxyClient()
				} else {
					reqClient = http.DefaultClient
				}
				// ua
				if conf.GlobalConfig.BrowserConf.UserAgent != "" {
					ctx.Request.Req().Header.Set("User-Agent", conf.GlobalConfig.BrowserConf.UserAgent)
				}
				// 加载响应：失败按配置重试（线性退避），错误不再静默吞掉
				loadErr := ctx.LoadResponse(reqClient, true)
				for attempt := 1; loadErr != nil && attempt <= conf.GlobalConfig.BrowserConf.Retry; attempt++ {
					time.Sleep(ratelimit.RetryBackoff(attempt))
					loadErr = ctx.LoadResponse(reqClient, true)
				}
				if loadErr != nil {
					log.Logger.Debugf("load response %s err: %s", ctx.Request.URL().String(), loadErr)
					return
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
				if ei.IsPage404(ctx.Request.URL().String()) {
					return
				}
				if ctx.Request.URL().String() == ei.GetPage404PageURL() {
					// 随机请求的url 404
					return
				}
				// 响应体二次提取：JS 接口入爬取队列、文本响应检密钥。
				// 提取在 body 编码存储之前做，--norrs 下同样生效。
				// hybrid fetcher 走同一个函数（extract_hook.go），两路语义一致。
				processExtractedResponse(ctx.Request.URL().String(), hijackContentType(ctx), ctx.Response.Payload().Body)
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
				if scope.IsInScope(pu.URL) {
					pushpendingNormalizeQueue(pu)
				} else {
					// 域外 URL 不入结果，但记录下来供 outscope 输出
					scope.RecordOutScope(pu.URL)
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
	// 常见路径探测（--fuzz 开启时）：与正常爬取并行，有效路径走现有管线
	if conf.GlobalConfig.FuzzConf.Enable {
		go func() {
			static.FuzzPaths(ctx, ei.Target, conf.GlobalConfig.FuzzConf.Dict, func(foundURL string) {
				log.Logger.Debugf("fuzz hit: %s", foundURL)
				PushUrlQueue(&UrlInfo{Url: foundURL, SourceType: "path fuzz", SourceUrl: "dict", Depth: 1})
			})
		}()
	}
	// 打开第一个tab页面 这里应该提交url管道任务
	PushUrlQueue(&UrlInfo{Url: ei.Target, Depth: 0, SourceType: "homePage", SourceUrl: "target"})
	page404url := ei.Target + "/" + utils.GenRandStr()
	ei.SetPage404PageURL(page404url)
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
	ei.Finish(ctx)
	ei.Launcher.Kill()
	ei.SaveResult()
	// 收尾保存断点状态（续爬起点）
	SaveCrawlState()
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

// hijackContentType 从劫持到的响应头里取 Content-Type。
func hijackContentType(ctx *rod.Hijack) string {
	for _, header := range ctx.Response.Payload().ResponseHeaders {
		if strings.EqualFold(header.Name, "Content-Type") {
			return header.Value
		}
	}
	return ""
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
