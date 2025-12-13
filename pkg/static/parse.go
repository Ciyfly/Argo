package static

import (
	"argo/pkg/log"
	"argo/pkg/utils"
	"strings"

	"github.com/go-rod/rod"
	xhtml "golang.org/x/net/html"
)

// urlAttributes 包含所有可能包含 URL 的 HTML 属性
// 扩展属性以提升 URL 发现能力
var urlAttributes = map[string]bool{
	// 标准属性
	"href":       true,
	"src":        true,
	"action":     true,
	"formaction": true,
	"cite":       true,
	"data":       true, // object 标签
	"poster":     true, // video 标签
	"srcset":     true, // 响应式图片
	"longdesc":   true,
	"profile":    true,
	"usemap":     true,
	"manifest":   true, // PWA manifest
	"content":    true, // meta refresh

	// 懒加载和动态加载属性
	"data-src":      true,
	"data-href":     true,
	"data-url":      true,
	"data-lazy":     true,
	"data-lazy-src": true,
	"data-original": true,
	"data-source":   true,
	"data-link":     true,
	"data-image":    true,
	"data-bg":       true,
	"data-poster":   true,

	// HTMX 属性 (现代 AJAX)
	"hx-get":    true,
	"hx-post":   true,
	"hx-put":    true,
	"hx-patch":  true,
	"hx-delete": true,

	// AngularJS 属性
	"ng-href":     true,
	"ng-src":      true,
	"ng-include":  true,
	"ng-template": true,

	// Vue.js 属性 (编译后)
	"xlink:href": true,

	// 其他常见属性
	"codebase":   true,
	"background": true, // 旧式背景图
	"dynsrc":     true, // IE 动态源
	"lowsrc":     true, // 低分辨率图片
}

// srcsetSeparators 用于解析 srcset 属性
var srcsetSeparators = []string{",", " "}

func getUrlByTag(t xhtml.Token, currentUrl string) []string {
	attr := t.Attr
	urls := []string{}
	for _, a := range attr {
		key := strings.ToLower(a.Key)
		val := strings.TrimSpace(a.Val)

		// 跳过 javascript: 和空值
		if val == "" || val == "#" || strings.HasPrefix(strings.ToLower(val), "javascript:") {
			continue
		}

		// 检查是否是 URL 属性
		if urlAttributes[key] {
			// 特殊处理 srcset 属性 (包含多个 URL)
			if key == "srcset" {
				srcsetUrls := parseSrcset(val, currentUrl)
				urls = append(urls, srcsetUrls...)
				continue
			}

			// 特殊处理 meta refresh content 属性
			if key == "content" && strings.Contains(strings.ToLower(val), "url=") {
				metaUrl := parseMetaRefresh(val)
				if metaUrl != "" {
					if resolved := HandlerUrl(metaUrl, currentUrl); resolved != "" {
						urls = append(urls, resolved)
					}
				}
				continue
			}

			log.Logger.Debugf("getUrlByTag attr=%s value=%s", key, val)
			if resolved := HandlerUrl(val, currentUrl); resolved != "" {
				urls = append(urls, resolved)
			}
		}

		// 检查 Vue.js 动态绑定 (:href, :src, v-bind:href, v-bind:src)
		if strings.HasPrefix(key, ":") || strings.HasPrefix(key, "v-bind:") {
			bindKey := strings.TrimPrefix(strings.TrimPrefix(key, "v-bind:"), ":")
			if bindKey == "href" || bindKey == "src" {
				// Vue 绑定的值可能是变量，尝试解析
				if !strings.ContainsAny(val, "{}()") {
					if resolved := HandlerUrl(val, currentUrl); resolved != "" {
						urls = append(urls, resolved)
					}
				}
			}
		}
	}
	return urls
}

// parseSrcset 解析 srcset 属性中的多个 URL
// 格式: "image1.jpg 1x, image2.jpg 2x" 或 "image1.jpg 480w, image2.jpg 800w"
func parseSrcset(srcset, currentUrl string) []string {
	var urls []string
	// 按逗号分割
	parts := strings.Split(srcset, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// 取第一个空格前的部分作为 URL
		fields := strings.Fields(part)
		if len(fields) > 0 {
			url := fields[0]
			if resolved := HandlerUrl(url, currentUrl); resolved != "" {
				urls = append(urls, resolved)
			}
		}
	}
	return urls
}

// parseMetaRefresh 解析 meta refresh 的 content 属性
// 格式: "5; url=http://example.com" 或 "0;url=http://example.com"
func parseMetaRefresh(content string) string {
	lower := strings.ToLower(content)
	idx := strings.Index(lower, "url=")
	if idx == -1 {
		return ""
	}
	url := strings.TrimSpace(content[idx+4:])
	// 移除可能的引号
	url = strings.Trim(url, "'\"")
	return url
}
// urlTags 需要解析 URL 属性的标签
var urlTags = map[string]bool{
	// 链接相关
	"a":      true,
	"link":   true,
	"area":   true,
	"base":   true,
	"frame":  true,
	"iframe": true,

	// 表单相关
	"form":   true,
	"button": true, // formaction 属性
	"input":  true, // formaction 属性

	// 媒体相关
	"img":    true,
	"video":  true,
	"audio":  true,
	"source": true,
	"track":  true,
	"embed":  true,
	"object": true,

	// 脚本和样式
	"script": true,

	// 其他
	"meta":       true,
	"blockquote": true, // cite 属性
	"q":          true, // cite 属性
	"ins":        true, // cite 属性
	"del":        true, // cite 属性
	"applet":     true, // codebase 属性
	"body":       true, // background 属性
	"table":      true, // background 属性
	"td":         true, // background 属性
	"th":         true, // background 属性
}

func ParseHtml(htmlStr, currentUrl string) []string {
	staticUrlList := []string{}
	// 解析 html 获取所有的 url
	tkn := xhtml.NewTokenizer(strings.NewReader(htmlStr))
	var tag string
	var inScript bool
	var scriptContent strings.Builder

	for {
		tt := tkn.Next()
		switch {
		case tt == xhtml.ErrorToken:
			return staticUrlList
		case tt == xhtml.StartTagToken:
			t := tkn.Token()
			tag = strings.ToLower(t.Data)

			// 检查是否进入 script 标签
			if tag == "script" {
				inScript = true
				scriptContent.Reset()
				// 同时解析 script 标签的 src 属性
				staticUrlList = append(staticUrlList, getUrlByTag(t, currentUrl)...)
			}

			// 解析需要提取 URL 的标签
			if urlTags[tag] {
				staticUrlList = append(staticUrlList, getUrlByTag(t, currentUrl)...)
			}

			// 解析所有标签的 data-* 属性 (可能包含 URL)
			for _, a := range t.Attr {
				key := strings.ToLower(a.Key)
				if strings.HasPrefix(key, "data-") && urlAttributes[key] {
					if resolved := HandlerUrl(a.Val, currentUrl); resolved != "" {
						staticUrlList = append(staticUrlList, resolved)
					}
				}
			}

		case tt == xhtml.EndTagToken:
			t := tkn.Token()
			if strings.ToLower(t.Data) == "script" && inScript {
				// 解析内联 script 内容中的 URL
				content := scriptContent.String()
				if content != "" {
					staticUrlList = append(staticUrlList, HandlerUrls(parseJs(content), currentUrl)...)
				}
				inScript = false
				scriptContent.Reset()
			}

		case tt == xhtml.TextToken:
			text := tkn.Token()
			if inScript {
				// 收集 script 内容
				scriptContent.WriteString(text.Data)
			} else {
				// 解析普通文本中的 URL
				staticUrlList = append(staticUrlList, HandlerUrls(findUrlMatch(text.String()), currentUrl)...)
			}

		case tt == xhtml.CommentToken:
			comment := tkn.Token()
			staticUrlList = append(staticUrlList, HandlerUrls(findUrlMatch(comment.String()), currentUrl)...)

		case tt == xhtml.SelfClosingTagToken:
			t := tkn.Token()
			tag = strings.ToLower(t.Data)
			if urlTags[tag] {
				staticUrlList = append(staticUrlList, getUrlByTag(t, currentUrl)...)
			}
		}
	}
}

func parseJs(content string) []string {
	// P0优化: 优先使用 JSluice AST 分析
	jsluiceURLs := ParseJSWithJSluice(content, "")
	if len(jsluiceURLs) > 0 {
		// 合并 JSluice 结果和正则结果
		regexURLs := findUrlMatch(content)
		seen := make(map[string]bool)
		result := make([]string, 0, len(jsluiceURLs)+len(regexURLs))

		for _, u := range jsluiceURLs {
			if !seen[u] {
				result = append(result, u)
				seen[u] = true
			}
		}
		for _, u := range regexURLs {
			if !seen[u] {
				result = append(result, u)
				seen[u] = true
			}
		}
		return result
	}

	// 回退到纯正则
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
		log.Logger.Warnf("ParseDom: failed to get current URL: %v", err)
		return nil
	}
	log.Logger.Debugf("parse dom %s", target)
	// 获取所有html
	htmlStr, err := page.HTML()
	if err != nil {
		log.Logger.Errorf("ParseDom error: %s", err)
		return nil
	}
	if htmlStr == "" {
		log.Logger.Warnf("ParseDom: empty HTML for %s", target)
		return nil
	}

	// 解析常规 URL
	urls := ParseHtml(htmlStr, target)

	// 解析表单并生成 URL
	formURLs := ExtractFormURLs(htmlStr, target)
	urls = append(urls, formURLs...)

	return urls
}
