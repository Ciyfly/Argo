package req

import (
	"argo/pkg/conf"
	"argo/pkg/log"
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

func getHttpClient(target, method string) (*http.Client, *http.Request) {
	request, _ := http.NewRequest(method, target, nil)
	request.Header.Set("User-Agent", WebUserAgent())
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		IdleConnTimeout:     3 * time.Second,
		MaxConnsPerHost:     5,
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: 10,
	}
	if conf.GlobalConfig.BrowserConf.Proxy != "" {
		proxy, _ := url.Parse(conf.GlobalConfig.BrowserConf.Proxy)
		transport.Proxy = http.ProxyURL(proxy)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second * 3, // 超时时间
	}
	return client, request
}

func CheckTarget(target string) bool {
	client, request := getHttpClient(target, "HEAD")
	resp, err := client.Do(request)
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

func GetResponse(target string) *http.Response {
	client, request := getHttpClient(target, "GET")
	resp, err := client.Do(request)
	if err != nil {
		return nil
	}
	if resp.StatusCode == http.StatusOK {
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

	proxySorted = strings.Split(conf.GlobalConfig.BrowserConf.Proxy, ":")

	if strings.Contains(conf.GlobalConfig.BrowserConf.Proxy, "http") {
		proxyURL, _ := url.Parse(conf.GlobalConfig.BrowserConf.Proxy)
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
