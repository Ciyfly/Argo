package extract

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"argo/pkg/static"
)

// 从 JS 内容中提取 API 接口。
//
// 三类通用规则，全部内容驱动、与具体站点无关：
//  1. 引号包裹的相对路径（"/api/user/list"、'/user/edit.php'）
//  2. fetch / axios / $.get 等请求调用的第一个字符串参数
//  3. JS 里写死的绝对 URL（复用 pkg/static 的 URL 正则）
//
// 提取结果解析成绝对 URL 后交给调用方入爬取队列，
// 后续的静态资源过滤、泛化去重、scope 判定全部走现有管线。

var (
	// 引号包裹、以 / 开头的路径。限制字符集避免把自然语言句子当路径
	quotedPathRegex = regexp.MustCompile(`["'](/[A-Za-z0-9\-._%/]{2,})["']`)
	// fetch/axios/$.ajax 调用的第一个字符串参数（相对或绝对均可）
	fetchCallRegex = regexp.MustCompile(`(?:fetch|axios(?:\.[A-Za-z]+)?|\$\.(?:get|post|put|delete|patch|ajax))\s*\(\s*["']([^"']+)["']`)
)

// 不值得入队爬取的前缀/形态
var skipPrefixes = []string{"data:", "blob:", "javascript:", "mailto:", "tel:", "{{", "webpack://", "chrome-extension:"}

// ExtractAPIPaths 从 JS 内容提取接口 URL（已去重、已解析为绝对地址）。
func ExtractAPIPaths(content, baseURL string) []string {
	candidates := map[string]bool{}

	for _, match := range quotedPathRegex.FindAllStringSubmatch(content, -1) {
		candidates[match[1]] = true
	}
	for _, match := range fetchCallRegex.FindAllStringSubmatch(content, -1) {
		candidates[match[1]] = true
	}
	// 绝对 URL（static 包的通用正则，导出复用避免两份正则各自漂移）
	for _, raw := range static.FindUrls(content) {
		candidates[raw] = true
	}

	base, _ := url.Parse(baseURL)
	appBase := appRootOf(base)
	results := make([]string, 0, len(candidates)*2)
	for candidate := range candidates {
		if resolved := resolveEndpoint(candidate, base); resolved != "" {
			results = append(results, resolved)
			// SPA 路由表写在 JS 里时 path 不含部署前缀（Vue Router base），
			// 如 JS 位于 /spa/assets/index.js 而路由是 /wizard/step-a——
			// 直接解析得 /wizard/step-a（404）。补一个按应用根（/spa）解析的
			// 变体：错的那个会被管线 404/预检过滤，对的命中。
			if appBase != nil {
				// 注意不能用 ResolveReference：候选是绝对路径（/x）时
				// base 的 path 会被整体替换，变体与原值恒等。直接拼接。
				withBase := appBase.Scheme + "://" + appBase.Host + strings.TrimSuffix(appBase.Path, "/") + candidate
				if withBase != resolved {
					results = append(results, withBase)
				}
			}
		}
	}
	sort.Strings(results)
	return results
}

// appRootOf 推断 SPA 部署根：assets 目录的上一级（/spa/assets/x.js → /spa）。
// 无 assets 特征时返回 nil（不产生变体）。
func appRootOf(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, seg := range segs {
		if seg == "assets" && i >= 1 {
			root := *u
			root.Path = "/" + strings.Join(segs[:i], "/")
			root.RawQuery = ""
			root.Fragment = ""
			return &root
		}
	}
	return nil
}

// resolveEndpoint 把候选字符串解析成绝对 URL，不合法/不值得爬的返回空。
func resolveEndpoint(candidate string, base *url.URL) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || len(candidate) > 2048 {
		return ""
	}
	lower := strings.ToLower(candidate)
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	// 模板占位、通配符（webpack chunk 名之类）不是真实地址
	if strings.ContainsAny(candidate, "*{}<>| ") {
		return ""
	}

	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" && parsed.Host == "" && parsed.Path == "" {
		return ""
	}
	if parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	// 相对地址基于 JS 文件自身的 URL 解析
	if parsed.Scheme == "" && base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}
