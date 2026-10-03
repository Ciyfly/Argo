package extract

import (
	"net/http"
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

// Endpoint 一条从 JS 里提取出的接口：绝对 URL + 识别到的请求方法。
// 方法来自调用写法（axios.post / $.put / fetch 的 method 选项）；
// 写法不体现方法（纯字符串路径、无选项的 fetch）时记 GET（浏览器默认）。
type Endpoint struct {
	URL    string
	Method string
}

var (
	// 引号包裹、以 / 开头的路径。限制字符集避免把自然语言句子当路径
	quotedPathRegex = regexp.MustCompile(`["'](/[A-Za-z0-9\-._%/]{2,})["']`)
	// fetch/axios/$.ajax 调用的第一个字符串参数（相对或绝对均可），方法未知
	fetchCallRegex = regexp.MustCompile(`(?:fetch|axios|\$\.ajax)\s*\(\s*["']([^"']+)["']`)
	// 带显式动词的调用：axios.post('/x')、$.get("/x")——动词就是方法
	verbCallRegex = regexp.MustCompile(`(?:axios|\$)\.(get|post|put|delete|patch)\s*\(\s*["']([^"']+)["']`)
	// fetch 的第二参 method 选项：fetch('/x', {method:'POST'})
	fetchMethodRegex = regexp.MustCompile(`(?i)fetch\s*\(\s*["']([^"']+)["']\s*,\s*\{[^{}]*method\s*:\s*["'](post|put|delete|patch|head|options)["']`)
	// 对象字面量里的 method+path/url 成对配置（路由表/接口配置表），
	// 两种属性顺序都认：{method:"POST",path:"/api/x"}、{url:"/api/x",method:"post"}
	methodThenPathRegex = regexp.MustCompile(`(?i)\{\s*method\s*:\s*["'](get|post|put|delete|patch|head|options)["']\s*,\s*(?:path|url|uri)\s*:\s*["']([^"']+)["']`)
	pathThenMethodRegex = regexp.MustCompile(`(?i)\{\s*(?:path|url|uri)\s*:\s*["']([^"']+)["']\s*,\s*method\s*:\s*["'](get|post|put|delete|patch|head|options)["']`)
)

// 不值得入队爬取的前缀/形态
var skipPrefixes = []string{"data:", "blob:", "javascript:", "mailto:", "tel:", "{{", "webpack://", "chrome-extension:"}

// ExtractAPIPaths 从 JS 内容提取接口（已去重、已解析为绝对地址）。
// 返回的 Method 只读调用写法（axios.post 等），未经真实请求验证。
func ExtractAPIPaths(content, baseURL string) []Endpoint {
	// 候选路径 → 方法。同一路径多处出现时用 preferredMethod 合并：
	// 显式动词（POST 等）优先于 GET/未知，避免纯字符串路径把方法冲掉。
	candidates := map[string]string{}

	mergeCandidate := func(path, method string) {
		candidates[path] = preferredMethod(candidates[path], method)
	}

	for _, match := range quotedPathRegex.FindAllStringSubmatch(content, -1) {
		mergeCandidate(match[1], http.MethodGet)
	}
	for _, match := range fetchCallRegex.FindAllStringSubmatch(content, -1) {
		mergeCandidate(match[1], http.MethodGet)
	}
	for _, match := range verbCallRegex.FindAllStringSubmatch(content, -1) {
		mergeCandidate(match[2], strings.ToUpper(match[1]))
	}
	for _, match := range fetchMethodRegex.FindAllStringSubmatch(content, -1) {
		mergeCandidate(match[1], strings.ToUpper(match[2]))
	}
	// 路由表/配置表：method 与 path/url 相邻的属性对（两种顺序）
	for _, match := range methodThenPathRegex.FindAllStringSubmatch(content, -1) {
		mergeCandidate(match[2], strings.ToUpper(match[1]))
	}
	for _, match := range pathThenMethodRegex.FindAllStringSubmatch(content, -1) {
		mergeCandidate(match[1], strings.ToUpper(match[2]))
	}
	// 绝对 URL（static 包的通用正则，导出复用避免两份正则各自漂移），方法未知
	for _, raw := range static.FindUrls(content) {
		mergeCandidate(raw, http.MethodGet)
	}

	base, _ := url.Parse(baseURL)
	appBase := appRootOf(base)
	results := make([]Endpoint, 0, len(candidates)*2)
	for candidate, method := range candidates {
		if resolved := resolveEndpoint(candidate, base); resolved != "" {
			results = append(results, Endpoint{URL: resolved, Method: method})
			// SPA 路由表写在 JS 里时 path 不含部署前缀（Vue Router base），
			// 如 JS 位于 /spa/assets/index.js 而路由是 /wizard/step-a——
			// 直接解析得 /wizard/step-a（404）。补一个按应用根（/spa）解析的
			// 变体：错的那个会被管线 404/预检过滤，对的命中。
			if appBase != nil {
				// 注意不能用 ResolveReference：候选是绝对路径（/x）时
				// base 的 path 会被整体替换，变体与原值恒等。直接拼接。
				withBase := appBase.Scheme + "://" + appBase.Host + strings.TrimSuffix(appBase.Path, "/") + candidate
				if withBase != resolved {
					results = append(results, Endpoint{URL: withBase, Method: method})
				}
			}
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].URL < results[j].URL })
	return results
}

// preferredMethod 合并同一候选路径在多处出现时的方法信息：
// 空/GET 让位给显式动词；两处都是动词且不一致（接口重载）保留先见者，
// 保证结果与匹配顺序无关。
func preferredMethod(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	if first == http.MethodGet {
		return second
	}
	return first
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
