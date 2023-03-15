package static

import (
	"argo/pkg/log"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/go-rod/rod"
	"golang.org/x/net/html"
)

var DynamicSuffix = []string{
	".php", ".asp", "aspx", ".action",
}

func handlerUrlPath(parsedURL *url.URL) string {
	pathList := strings.Split(parsedURL.Path, "/")
	return parsedURL.Scheme + "://" + parsedURL.Host + strings.Join(pathList[:len(pathList)-1], "/") + "/"
}

func handlerDynamicUrl(target string) string {
	if target[len(target)-1:] == "/" {
		return target
	}
	parsedURL, _ := url.Parse(target)
	// http://testphp.vulnweb.com/hpp/?pp=12
	if strings.Contains(target, "?") {
		return handlerUrlPath(parsedURL)
	}
	suffix := filepath.Ext(parsedURL.Path)
	for _, s := range DynamicSuffix {
		if suffix == s {
			return handlerUrlPath(parsedURL)
		}
	}
	return target

}

func ParseHtml(htmlStr, currentUrl string) []string {
	staticUrlList := []string{}
	// 获取到js 给parseJs
	tkn := html.NewTokenizer(strings.NewReader(htmlStr))
	var tag string
	for {
		tt := tkn.Next()
		switch {
		case tt == html.ErrorToken:
			return staticUrlList
		case tt == html.StartTagToken:
			t := tkn.Token()
			tag = t.Data
			if tag == "a" {
				attr := t.Attr
				for _, a := range attr {
					if a.Key == "href" && !strings.Contains(a.Val, "javascript") && !strings.Contains(a.Val, "#") {
						staticUrlList = append(staticUrlList, HandlerUrl(a.Val, currentUrl))
					}
				}
			} else if tag == "script" {

				staticUrlList = append(staticUrlList, HandlerUrls(parseJs(t.String()), currentUrl)...)
			}
		case tt == html.CommentToken:
			comment := tkn.Token()
			staticUrlList = append(staticUrlList, HandlerUrls(findUrlMatch(comment.String()), currentUrl)...)
		case tt == html.TextToken:
			text := tkn.Token()
			staticUrlList = append(staticUrlList, HandlerUrls(findUrlMatch(text.String()), currentUrl)...)
		}
	}
}

func parseJs(content string) []string {
	return findUrlMatch(content)
}

func HandlerUrl(urlStr, currentUrl string) string {
	if !strings.Contains(urlStr, "http") {
		if urlStr[:1] == "/" {
			// 如果 href开头是/ 那么就是以host的路由拼接 否则是以当前路由做拼接
			parsedURL, _ := url.Parse(currentUrl)
			return parsedURL.Scheme + "://" + parsedURL.Host + urlStr
		} else {
			// path/index.php?id=1 -> /path/new.php
			return handlerDynamicUrl(currentUrl) + urlStr
		}
	}
	return urlStr
}

func HandlerUrls(urls []string, currentUrl string) []string {
	newUrls := []string{}
	for _, urlStr := range urls {
		if !strings.Contains(urlStr, "http") {
			// xxx.php
			if urlStr[:1] == "/" {
				// /index.php -> host/index.php
				parsedURL, _ := url.Parse(currentUrl)
				newUrls = append(newUrls, parsedURL.Scheme+"://"+parsedURL.Host+urlStr)

			} else {
				// path/index.php?id=1 -> /path/new.php
				newUrls = append(newUrls, handlerDynamicUrl(currentUrl)+urlStr)
			}
		} else {
			newUrls = append(newUrls, urlStr)
		}
	}
	return newUrls
}

func ParseDom(page *rod.Page) []string {
	info, err := page.Info()
	if err != nil {
		return nil
	}
	log.Logger.Debugf("parse dom %s", info.URL)
	// 获取所有html
	htmlStr, err := page.HTML()
	parsedURL, _ := url.Parse(info.URL)
	strippedURL := parsedURL.Scheme + "://" + parsedURL.Host + parsedURL.Path
	if strippedURL[len(strippedURL)-1:] != "/" {
		strippedURL = parsedURL.Scheme + "://" + parsedURL.Host + "/"
	}
	if err != nil {
		log.Logger.Errorf("parseDemo error: %s", err)
	}
	return ParseHtml(htmlStr, strippedURL)
}
