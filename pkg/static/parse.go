package static

import (
	"argo/pkg/log"
	"argo/pkg/utils"
	"strings"

	"github.com/go-rod/rod"
	xhtml "golang.org/x/net/html"
)

func getUrlByTag(t xhtml.Token, currentUrl string) []string {
	attr := t.Attr
	urls := []string{}
	for _, a := range attr {
		if (a.Key == "href" || a.Key == "src" || a.Key == "action") && !strings.Contains(a.Val, "javascript") && a.Val != "#" {
			log.Logger.Debugf("getUrlByTag %s", a.Val)
			if resolved := HandlerUrl(a.Val, currentUrl); resolved != "" {
				urls = append(urls, resolved)
			}
		}
	}
	return urls
}
func ParseHtml(htmlStr, currentUrl string) []string {
	staticUrlList := []string{}
	// 解析 html 获取所有的 url
	tkn := xhtml.NewTokenizer(strings.NewReader(htmlStr))
	var tag string
	for {
		tt := tkn.Next()
		switch {
		case tt == xhtml.ErrorToken:
			return staticUrlList
		case tt == xhtml.StartTagToken:
			t := tkn.Token()
			tag = t.Data
			// 标签解析对应属性值
			if tag == "a" || tag == "link" || tag == "frame" || tag == "form" {
				staticUrlList = append(getUrlByTag(t, currentUrl), staticUrlList...)
			} else if tag == "script" {
				staticUrlList = append(staticUrlList, HandlerUrls(parseJs(t.String()), currentUrl)...)
			}
		case tt == xhtml.CommentToken:
			comment := tkn.Token()
			staticUrlList = append(staticUrlList, HandlerUrls(findUrlMatch(comment.String()), currentUrl)...)
		case tt == xhtml.TextToken:
			text := tkn.Token()
			staticUrlList = append(staticUrlList, HandlerUrls(findUrlMatch(text.String()), currentUrl)...)
		}
	}
}

func parseJs(content string) []string {
	return findUrlMatch(content)
}

func HandlerUrl(urlStr, currentUrl string) string {
	canonical, err := utils.CanonicalizeURL(urlStr, currentUrl)
	if err != nil {
		if log.Logger != nil {
			log.Logger.Debugf("[url skip] reason=%s value=%s", err.Error(), urlStr)
		}
		return ""
	}
	return canonical
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
	if err != nil {
		log.Logger.Errorf("parseDemo error: %s", err)
		return nil
	}
	return ParseHtml(htmlStr, target)
}
