package req

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
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
		log.Logger.Errorf("req error: %s", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true
	}
	log.Logger.Error("req !!!!: ")
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
