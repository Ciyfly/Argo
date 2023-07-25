package static

import (
	"argo/pkg/log"
	"argo/pkg/utils"
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

func getUrlByTag(t html.Token, currentUrl string) []string {
	attr := t.Attr
	urls := []string{}
	for _, a := range attr {
		if (a.Key == "href" || a.Key == "src" || a.Key == "action") && !strings.Contains(a.Val, "javascript") && a.Val != "#" {
			log.Logger.Debugf("getUrlByTag %s", a.Val)
			urls = append(urls, HandlerUrl(a.Val, currentUrl))
		}
	}
	return urls
}
func ParseHtml(htmlStr, currentUrl string) []string {
	staticUrlList := []string{}
	// 解析 html 获取所有的 url
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
			// 标签解析对应属性值
			if tag == "a" || tag == "link" || tag == "frame" || tag == "form" {
				staticUrlList = append(getUrlByTag(t, currentUrl), staticUrlList...)
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

func absUrl(urlStr string) string {
	u, _ := url.Parse(urlStr)
	p := filepath.Clean(u.Path)
	base := filepath.Base(p)
	dir := filepath.Dir(p)
	abs, _ := filepath.Abs(filepath.Join(dir, base))
	u.Path = abs
	if urlStr[len(urlStr)-1:] == "/" {
		return u.String() + "/"
	}
	return u.String()
}

// 判断一个 URL 是否为一个有效的 URL
func isValidURL(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	if u.Scheme == "" || u.Host == "" {
		return false
	}
	return true
}

// 根据当前 URL 和相对路径，生成一个有效的 URL
func resolveRelativeURL(currentUrl, relativePath string) string {
	if strings.Contains(relativePath, "http") {
		return relativePath
	}
	parsedURL, _ := url.Parse(currentUrl)
	basePath := strings.TrimSuffix(parsedURL.Path, filepath.Base(parsedURL.Path))
	return parsedURL.Scheme + "://" + parsedURL.Host + filepath.Join(basePath, relativePath)
}

func HandlerUrl(urlStr, currentUrl string) string {
	parsedURL, _ := url.Parse(currentUrl)
	if !strings.Contains(urlStr, "http") && !strings.HasPrefix(urlStr, "//") {
		if len(urlStr) == 0 {
			return ""
		}
		// mat1.gtimg.com/www/mb/js/portal/mi.MiniNav__v1.0.0.js
		if strings.HasSuffix(urlStr, ".js") {
			return parsedURL.Scheme + "://" + parsedURL.Host + urlStr
		}
		if urlStr[:1] == "/" {
			// 如果 href 开头是 /，那么就是以 host 的路由拼接，否则是以当前路由做拼接
			return parsedURL.Scheme + "://" + parsedURL.Host + urlStr
		} else {
			// path/index.php?id=1 -> /path/new.php
			newUrl := handlerDynamicUrl(currentUrl) + urlStr
			// 处理相对路径
			if strings.Contains(newUrl, "../") {
				newUrl = resolveRelativeURL(currentUrl, newUrl)
			}
			// vm.gtimg.cn/thumbplayer/superplayer/1.15.22/superplayer.js
			if strings.Count(newUrl, ".") > 3 {
				newUrl = parsedURL.Scheme + "://" + "/" + newUrl
			}
			// 判断 newUrl 是否为一个有效的 URL，如果不是，则根据 currentUrl 构建一个有效的 URL
			if !isValidURL(newUrl) {
				newUrl = parsedURL.Scheme + "://" + urlStr
			}
			return newUrl
		}
	}

	if strings.HasPrefix(urlStr, "//") {
		return parsedURL.Scheme + ":" + urlStr
	}
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		return urlStr
	}
	return ""
}

// 处理多个 URL，返回处理后的 URL 列表
func HandlerUrls(urls []string, currentUrl string) []string {
	result := []string{}
	for _, url := range urls {
		log.Logger.Debugf("HandlerUrl before%s", url)
		newUrl := HandlerUrl(url, currentUrl)
		log.Logger.Debugf("HandlerUrl after%s", newUrl)
		if newUrl != "" && !utils.Contains(result, newUrl) {
			result = append(result, newUrl)
		}
	}
	return result
}

func ParseDom(page *rod.Page) []string {
	target, err := utils.GetCurrentUrlByPage(page)
	if err != nil {
		return nil
	}
	log.Logger.Debugf("parse dom %s", target)
	// 获取所有html
	htmlStr, err := page.HTML()
	parsedURL, _ := url.Parse(target)
	strippedURL := parsedURL.Scheme + "://" + parsedURL.Host + parsedURL.Path
	// 如果url不是以 / 结尾，那么就加上 /
	if strippedURL[len(strippedURL)-1:] != "/" {
		strippedURL = parsedURL.Scheme + "://" + parsedURL.Host + "/"
	}
	if err != nil {
		log.Logger.Errorf("parseDemo error: %s", err)
	}
	return ParseHtml(htmlStr, strippedURL)
}
