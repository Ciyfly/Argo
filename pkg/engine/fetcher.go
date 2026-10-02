package engine

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/req"
	"argo/pkg/static"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// hybrid 引擎：文档类页面用 Go http 抓取 + 离线解析，不开浏览器。
//
// katana 标准引擎的速度优势来自「能不用浏览器就不用」。Argo 的内容预检
// 已让非文档 URL 走 Go 侧；这里把 HTML 文档页也分流（--engine hybrid）：
//   - depth 0 首页照旧浏览器（登录/交互入口必须真实浏览器）
//   - depth >= 1 文档页 Go 抓取：记结果 + 离线解析子链接 + 二次提取
//   - 空壳页（JS 渲染依赖）升级浏览器处理
//
// 去重：与浏览器路径共用 normalize 管线，两路结果天然合并。

// shellDetectMinLinks / shellDetectMinSize 空壳判定阈值。
// 「同域 <a> 少且引了 JS」或「HTML 极小且有 SPA 挂载点」都是
// 内容靠 JS 渲染的通用信号，与具体站点无关。
const (
	shellDetectMinLinks = 3
	shellDetectMinSize  = 5 * 1024
)

var (
	scriptSrcRegex = regexp.MustCompile(`<script[^>]+src\s*=`)
	spaMountRegex  = regexp.MustCompile(`<div[^>]+id\s*=\s*["']?(app|root|main)["']?`)
	pageLinkRegex  = regexp.MustCompile(`<a\s[^>]*href\s*=\s*["']([^"'#]+)`)
)

// FetchDocumentPage hybrid 模式下用 Go 抓取一个文档页并离线处理。
// 返回是否升级为浏览器处理（空壳页）。
func FetchDocumentPage(uif *UrlInfo) bool {
	client, request := fetcherClient(), newGetRequest(uif.Url)
	resp, err := client.Do(request)
	if err != nil {
		log.Logger.Debugf("hybrid fetch %s err: %s", uif.Url, err)
		// 抓取失败保守升级浏览器（可能需要浏览器态才能访问）
		return true
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBodySize))
	if err != nil {
		log.Logger.Debugf("hybrid fetch %s read err: %s", uif.Url, err)
		return true
	}
	contentType := resp.Header.Get("Content-Type")
	// 中途变成非 HTML（重定向到登录页等仍 text/html 没问题；json 等直接按接口记录）
	if contentType != "" && !req.IsHTMLContentType(contentType) {
		recordFetchedURL(uif.Url, resp.StatusCode, resp.Header)
		return false
	}

	// 空壳判定：升级浏览器
	if looksLikeShell(body, uif.Url) {
		return true
	}

	// 记录结果（带真实响应头与状态码）
	recordFetchedURL(uif.Url, resp.StatusCode, resp.Header)

	// 二次提取（与 hijack 同一函数，语义一致）
	processExtractedResponse(uif.Url, contentType, body)

	// 离线解析子链接入队
	for _, childURL := range static.ParseHtml(string(body), uif.Url) {
		PushUrlQueue(&UrlInfo{Url: childURL, SourceType: "static parse", SourceUrl: uif.Url, Depth: uif.Depth + 1})
	}
	return false
}

// maxFetchBodySize 单页抓取上限（与 extract 的扫描上限同量级）
const maxFetchBodySize = 4 * 1024 * 1024

// recordFetchedURL 把 Go 抓到的页面记为结果（与 hijack 记录同一管线）。
func recordFetchedURL(pageURL string, status int, headers http.Header) {
	pu := &PendingUrl{
		URL:             pageURL,
		Method:          http.MethodGet,
		Headers:         http.Header{},
		Status:          status,
		ResponseHeaders: headers,
	}
	if !conf.GlobalConfig.NoReqRspStr {
		// Go 侧没有重复抓 body 存证，response body 编码仅浏览器路径有——保持字段为空
		pu.ResponseBody = ""
	}
	pushpendingNormalizeQueue(pu)
}

// looksLikeShell 判断 HTML 是否为「JS 渲染空壳」。
func looksLikeShell(body []byte, pageURL string) bool {
	html := string(body)
	if len(strings.TrimSpace(html)) < shellDetectMinSize && spaMountRegex.MatchString(html) {
		return true
	}
	links := pageLinkRegex.FindAllStringSubmatch(html, -1)
	sameHostLinks := 0
	host := hostnameOfTarget(pageURL)
	for _, m := range links {
		if strings.Contains(m[1], host) || strings.HasPrefix(m[1], "/") {
			sameHostLinks++
		}
	}
	return sameHostLinks < shellDetectMinLinks && scriptSrcRegex.MatchString(html)
}

// fetcherClient 文档抓取 client：标准超时、跟随重定向、代理与全局配置一致。
func fetcherClient() *http.Client {
	transport := &http.Transport{}
	if proxyAddr := req.EffectiveProxy(); proxyAddr != "" {
		if proxyURL, err := url.Parse(proxyAddr); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func newGetRequest(pageURL string) *http.Request {
	request, _ := http.NewRequest(http.MethodGet, pageURL, nil)
	request.Header.Set("User-Agent", req.WebUserAgent())
	return request
}

// hybridEnabled 判断当前是否 hybrid 引擎模式。
func hybridEnabled() bool {
	return strings.EqualFold(conf.GlobalConfig.BrowserConf.Engine, "hybrid")
}
