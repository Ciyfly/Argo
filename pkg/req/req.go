package req

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/ratelimit"
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// WebUserAgent returns the chrome-web user agent
func WebUserAgent() string {
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 11_1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Safari/537.36"
}

// EffectiveProxy 返回 Go 侧请求应使用的代理地址。
// pushproxy（被动扫描器）优先于普通代理——与浏览器侧的优先级保持一致，
// 否则探测请求会绕过扫描器造成漏扫。
func EffectiveProxy() string {
	if conf.GlobalConfig.BrowserConf.PushProxy != "" {
		return conf.GlobalConfig.BrowserConf.PushProxy
	}
	return conf.GlobalConfig.BrowserConf.Proxy
}

func getHttpClient(target, method string) (*http.Client, *http.Request) {
	request, _ := http.NewRequest(method, target, nil)
	request.Header.Set("User-Agent", WebUserAgent())
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxConnsPerHost:     5,
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: 10,
	}
	if effective := EffectiveProxy(); effective != "" {
		proxyURL, _ := url.Parse(effective)
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	if conf.GlobalConfig.BrowserConf.UserAgent != "" {
		request.Header.Set("User-Agent", conf.GlobalConfig.BrowserConf.UserAgent)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second * 10, // 超时时间
	}
	return client, request
}

// DoWithRetry 执行请求并按 retries 重试（线性退避）。
// 只用于 GET 等无 body 的静态探测请求（robots.txt / sitemap.xml 等）——
// 带 body 的请求重放需要重建 body，当前调用方没有这个场景。
func DoWithRetry(client *http.Client, request *http.Request, retries int) (*http.Response, error) {
	resp, err := client.Do(request)
	for attempt := 1; err != nil && attempt <= retries; attempt++ {
		time.Sleep(ratelimit.RetryBackoff(attempt))
		resp, err = client.Do(request)
	}
	return resp, err
}

// ProbeResult 是一次轻量探测的结果。
type ProbeResult struct {
	StatusCode  int
	ContentType string
	Headers     http.Header
	Ok          bool
}

// ProbeContentType 用 Go 侧 http（不开浏览器）探测 URL 的响应类型。
//
// 用于「无文档后缀的 URL」分流：json/xml 等接口开浏览器访问纯属浪费预算，
// 而无后缀的页面（SPA 路由 /about、/spa/shop）又必须开浏览器才能扩散链接。
// 只有内容类型能可靠区分这两者——按内容驱动，不猜路径。
// 失败（超时/连接错误）时 Ok=false，调用方保守回退为开 tab。
func ProbeContentType(target string) ProbeResult {
	client, request := getHttpClient(target, "GET")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := DoWithRetry(client, request, conf.GlobalConfig.BrowserConf.Retry)
	if err != nil {
		log.Logger.Debugf("probe %s err: %s", target, err)
		return ProbeResult{Ok: false}
	}
	defer resp.Body.Close()
	return ProbeResult{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Headers:     resp.Header,
		Ok:          true,
	}
}

// IsHTMLContentType 判断内容类型是否 HTML 页面。
func IsHTMLContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
}

func CheckTarget(target string) bool {
	client, request := getHttpClient(target, "GET")
	resp, err := DoWithRetry(client, request, conf.GlobalConfig.BrowserConf.Retry)
	if err != nil {
		log.Logger.Debugf("req error: %s", err)
		return false
	}
	defer resp.Body.Close()
	return !(resp.StatusCode == http.StatusNotFound ||
		resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusServiceUnavailable ||
		resp.StatusCode == http.StatusGatewayTimeout)
}

// GetResponseWithBody 发起一次 GET 请求并返回响应，调用方负责关闭 resp.Body。
// 只有状态码为 200 时才返回响应，其余情况（含请求错误）返回 nil。
//
// fix: 这里原先写成了「状态码是 200 就返回 nil」，逻辑正好相反，
// 导致 robots.txt / sitemap.xml 的解析永远拿不到内容（功能完全失效）。
func GetResponseWithBody(target string) *http.Response {
	client, request := getHttpClient(target, "GET")
	resp, err := DoWithRetry(client, request, conf.GlobalConfig.BrowserConf.Retry)
	if err != nil {
		log.Logger.Debugf("get response %s err: %s", target, err)
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		log.Logger.Debugf("get response %s status: %d", target, resp.StatusCode)
		resp.Body.Close()
		return nil
	}
	return resp
}

func AbsoluteURL(path, scheme string) string {
	if strings.HasPrefix(path, "#") {
		return ""
	}

	absURL, err := url.Parse(path)
	if err != nil {
		return ""
	}

	absURL.Fragment = ""
	if absURL.Scheme == "//" {
		absURL.Scheme = scheme
	}

	final := absURL.String()
	return final
}

func GetProxyClient() *http.Client {
	httpClient := http.Client{}
	var auth string
	var proxySorted []string

	proxyAddr := EffectiveProxy()
	proxySorted = strings.Split(proxyAddr, ":")

	if strings.Contains(proxyAddr, "http") {
		proxyURL, _ := url.Parse(proxyAddr)
		if len(proxySorted) > 3 {
			// 有用户名密码
			auth = strings.Split(proxySorted[1], "//")[1] + ":" + strings.Split(proxySorted[2], "@")[0]
			basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
			hdr := http.Header{}
			hdr.Add("Proxy-Authorization", basicAuth)
			transport := &http.Transport{
				Proxy:              http.ProxyURL(proxyURL),
				ProxyConnectHeader: hdr,
				IdleConnTimeout:    5 * time.Second,
			}
			httpClient.Transport = transport
		} else {
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			}
			httpClient.Transport = transport
		}
		return &httpClient
	} else {
		var auth *proxy.Auth
		if len(proxySorted) > 3 {
			auth = &proxy.Auth{
				User:     strings.Split(proxySorted[1], "//")[1],
				Password: strings.Split(proxySorted[2], "@")[0],
			}
		}
		dialer, err := proxy.SOCKS5("tcp", "PROXY_IP", auth, proxy.Direct)
		if err != nil {
			log.Logger.Errorf("proxt socks err: %s", err)
		}
		tr := &http.Transport{Dial: dialer.Dial}
		return &http.Client{
			Transport: tr,
		}
	}
}
