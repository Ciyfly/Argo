package engine

import (
	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/utils"
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/urfave/cli/v2"
)

type EngineInfo struct {
	Browser   *rod.Browser
	Options   conf.BrowserConf
	Launcher  *launcher.Launcher
	CloseChan chan int
	Target    string
}

type UrlInfo struct {
	Url        string
	SourceType string
	Match      string
	SourceUrl  string
}

var EngineInfoData *EngineInfo

func InitEngine(c *cli.Context) {
	target := c.String("target")
	conf.LoadConfig()
	// 合并 命令行与 yaml
	conf.MergeArgs(c)
	// 初始化 js注入插件
	inject.LoadScript()
	// 初始化 登录插件
	login.InitLoginAuto()
	// 初始化 泛化模块
	InitNormalize()
	// 初始化 结果处理模块
	InitResultHandler()
	// 初始化tab控制携程池
	InitTabPool()
	// 初始化 urls队列 tab新建
	InitController(target)
	// 初始化静态资源过滤
	InitFilter()
	// 初始化浏览器
	EngineInfoData = InitBrowser(target)
}

func InitBrowser(target string) *EngineInfo {
	// 初始化
	browser := rod.New().Timeout(time.Duration(conf.GlobalConfig.BrowserConf.BrowserTimeout) * time.Second)
	// 启动无痕
	if conf.GlobalConfig.BrowserConf.Trace {
		browser = browser.Trace(true)
	}
	// options := launcher.New().Devtools(true)
	//  NoSandbox fix linux下root运行报错的问题
	options := launcher.New().NoSandbox(true).Headless(true)
	if conf.GlobalConfig.BrowserConf.UnHeadless {
		options = options.Delete("--headless")
		browser = browser.SlowMotion(time.Duration(conf.GlobalConfig.AutoConf.Slow) * time.Second)
	}
	browser = browser.ControlURL(options.MustLaunch()).MustConnect().NoDefaultDevice().MustIncognito()

	closeChan := make(chan int, 1)
	return &EngineInfo{
		Browser:   browser,
		Options:   conf.GlobalConfig.BrowserConf,
		Launcher:  options,
		CloseChan: closeChan,
		Target:    target}
}

func Start() {
	log.Logger.Debugf("tab timeout: %d", conf.GlobalConfig.BrowserConf.TabTimeout)
	log.Logger.Debugf("browser timeout: %d", conf.GlobalConfig.BrowserConf.BrowserTimeout)
	// hook 请求响应获取所有异步请求
	router := EngineInfoData.Browser.HijackRequests()
	defer router.Stop()
	router.MustAdd("*", func(ctx *rod.Hijack) {
		var reqStr []byte
		reqStr, _ = httputil.DumpRequest(ctx.Request.Req(), true)
		save, body, _ := copyBody(ctx.Request.Req().Body)
		saveStr, _ := ioutil.ReadAll(save)
		ctx.Request.Req().Body = body
		ctx.LoadResponse(http.DefaultClient, true)
		if ctx.Response.Payload().ResponseCode == 404 {
			return
		}
		pu := &PendingUrl{
			URL:             ctx.Request.URL().String(),
			Method:          ctx.Request.Method(),
			Host:            ctx.Request.Req().Host,
			Headers:         ctx.Request.Req().Header,
			Data:            string(saveStr),
			ResponseHeaders: transformHttpHeaders(ctx.Response.Payload().ResponseHeaders),
			ResponseBody:    utils.EncodeBase64(ctx.Response.Payload().Body),
			RequestStr:      utils.EncodeBase64(reqStr),
			Status:          ctx.Response.Payload().ResponseCode,
		}
		pushpendingNormalizeQueue(pu)
	})
	go router.Run()
	// 打开第一个tab页面 这里应该提交url管道任务
	// PushStaticUrl(EngineInfoData.Target, 0)
	EngineInfoData.NewTab(&UrlInfo{Url: EngineInfoData.Target}, 0)
	// 结束
	// 0. 首页解析完成
	// 1. url管道没有数据
	// 2. 携程池任务完成
	// 3. 没有tab页面存在
	<-EngineInfoData.CloseChan
	log.Logger.Debug("front page over")
	urlsQueueEmpty()
	log.Logger.Debug("urlsQueueEmpty over")
	for {
		if TabPool.Running() == 0 {
			break
		}
	}
	TabWg.Wait()
	TabPool.Release()
	log.Logger.Debug("tabPool over")
	if EngineInfoData.Browser != nil {
		closeErr := EngineInfoData.Browser.Close()
		if closeErr != nil {
			log.Logger.Errorf("browser close err: %s", closeErr)

		} else {
			log.Logger.Debug("browser close over")
		}
	}
	CloseNormalizeQueue()
	PendingNormalizeQueueEmpty()
	log.Logger.Debug("pendingNormalizeQueueEmpty over")
	SaveResult(EngineInfoData.Target)
	EngineInfoData.Launcher.Kill()
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
