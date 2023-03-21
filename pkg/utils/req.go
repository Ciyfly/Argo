/*
 * @Author: Recar
 * @Date: 2023-03-16 21:13:52
 * @LastEditors: Recar
 * @LastEditTime: 2023-03-16 21:14:30
 */

package utils

import (
	"crypto/tls"
	"fmt"
	"github.com/projectdiscovery/fastdialer/fastdialer"
	"github.com/projectdiscovery/retryablehttp-go"
	errorutil "github.com/projectdiscovery/utils/errors"
	proxyutil "github.com/projectdiscovery/utils/http/proxy"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func CheckTarget(target string) bool {
	client := http.Client{
		Timeout: time.Second * 5,
	}
	resp, err := client.Get(target)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true
	}
	return false
}

type RedirectCallback func(resp *http.Response, depth int)

// BuildHttpClient builds a http client based on a profile
func BuildHttpClient(proxy string, redirectCallback RedirectCallback) (*retryablehttp.Client, error) {
	dialerOpts := fastdialer.DefaultOptions

	dialer, err := fastdialer.NewDialer(dialerOpts)
	if err != nil {
		return nil, err
	}

	// Single Host
	retryablehttpOptions := retryablehttp.DefaultOptionsSingle
	retryablehttpOptions.RetryMax = 1
	transport := &http.Transport{
		DialContext:         dialer.Dial,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     100,
		TLSClientConfig: &tls.Config{
			Renegotiation:      tls.RenegotiateOnceAsClient,
			InsecureSkipVerify: true,
		},
		DisableKeepAlives: false,
	}

	// Attempts to overwrite the dial function with the socks proxied version
	if proxyURL, err := url.Parse(proxy); proxy != "" && err == nil {
		if ok, err := proxyutil.IsBurp(proxy); err == nil && ok {
			transport.TLSClientConfig.MaxVersion = tls.VersionTLS12
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	client := retryablehttp.NewWithHTTPClient(&http.Client{
		Transport: transport,
		Timeout:   time.Duration(5) * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 10 {
				return errorutil.New("stopped after 10 redirects")
			}
			if redirectCallback != nil {
				redirectCallback(req.Response, 2)
			}
			return nil
		},
	}, retryablehttpOptions)
	client.CheckRetry = retryablehttp.HostSprayRetryPolicy()
	return client, nil
}

// WebUserAgent returns the chrome-web user agent
func WebUserAgent() string {
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 11_1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Safari/537.36"
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
