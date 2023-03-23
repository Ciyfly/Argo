package req

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"crypto/tls"
	"encoding/base64"
	"fmt"
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

func getHttpTransport(target string) (*http.Transport, *http.Request) {
	request, _ := http.NewRequest("GET", target, nil)
	request.Header.Set("Connection", "keep-alive")
	request.Header.Set("User-Agent", WebUserAgent())
	if conf.GlobalConfig.BrowserConf.Proxy != "" {
		proxy, _ := url.Parse(conf.GlobalConfig.BrowserConf.Proxy)
		return &http.Transport{
			Proxy:           http.ProxyURL(proxy),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}, request
	} else {
		return &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}, request
	}
}

func CheckTarget(target string) bool {
	transport, request := getHttpTransport(target)
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second * 3, //超时时间
	}
	resp, err := client.Do(request)
	if err != nil {
		log.Logger.Debugf("req error: %s", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true
	}
	return false
}

func GetResponse(target string) *http.Response {
	transport, request := getHttpTransport(target)
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second * 3, //超时时间
	}
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
		fmt.Println(err)
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
