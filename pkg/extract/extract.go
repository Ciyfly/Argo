package extract

import (
	"strings"
)

// 响应体二次提取：从 JS/文本响应中提取 API 接口与密钥泄漏。
//
// 提取是纯内容驱动的通用规则（正则表 + 相对路径解析），
// 不绑定任何站点/靶场；自定义密钥规则经 config.yml 的 extract.secret_rules 注入。

// MaxScanBodySize 超过该大小的响应体跳过提取（超大文件正则扫描得不偿失，
// 且 rod 的 Payload().Body 对超大响应本身可能不完整）。
const MaxScanBodySize = 2 * 1024 * 1024

// ResponseInput 是一次待提取的响应。由调用方（engine hijack）组装，
// 通过回调返回结果，extract 包不依赖 engine，便于单测。
type ResponseInput struct {
	URL         string
	ContentType string
	Body        []byte
	// OnEndpoint 收到一条从 JS 中提取出的接口（绝对 URL + 调用写法里的方法，
	// 方法识别不出时为 GET；调用方负责记结果/入爬取队列）
	OnEndpoint func(endpoint Endpoint)
	// OnSecret 收到一条密钥命中
	OnSecret func(finding SecretFinding)
}

// ProcessResponse 按响应类型分发提取。
// JS 响应：接口提取（绝对 URL + 相对路径 + fetch 调用）。
// HTML 响应：同样跑完整提取——内联 script 里的路径数组/onclick 跳转等
//
//	只存在于 JS 字符串里，DOM 解析永远看不到（实测多级菜单的第三级
//	只写在 JS 数组字面量里，任何交互都不揭示它）；
//	与 DOM 解析重复的部分由管线的去重吸收。
//
// 文本响应：密钥检测。
func (in *ResponseInput) Process() {
	if len(in.Body) == 0 || len(in.Body) > MaxScanBodySize {
		return
	}
	switch {
	case IsJSResponse(in.URL, in.ContentType), IsHTMLResponse(in.ContentType):
		for _, endpoint := range ExtractAPIPaths(string(in.Body), in.URL) {
			if in.OnEndpoint != nil {
				in.OnEndpoint(endpoint)
			}
		}
	}
	// 密钥检测覆盖所有文本类响应（JS/HTML/json/xml/text）
	if IsTextResponse(in.URL, in.ContentType) {
		for _, finding := range ScanSecrets(in.URL, in.Body) {
			if in.OnSecret != nil {
				in.OnSecret(finding)
			}
		}
	}
}

// IsDocumentURL 判断提取出的地址是否值得开 tab 访问。
// JS 里解析出的接口（/api/...）直接记为结果即可——开 tab 访问 JSON 接口
// 不会带来新链接，只会把浏览器预算烧掉（爬虫基线实测：全量入队会把
// browsertimeout 吃爆，交互页面被饿死，must_rate 反而下降）。
// 只有长得像文档的地址（.html/.php 等页面后缀）才值得继续扩散。
func IsDocumentURL(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	// 去掉 query 再看后缀
	if idx := strings.IndexAny(lower, "?#"); idx >= 0 {
		lower = lower[:idx]
	}
	for _, suffix := range []string{".html", ".htm", ".php", ".asp", ".aspx", ".jsp", ".jspx"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// IsJSResponse 按 content-type 或 URL 后缀判断是否 JS。
func IsJSResponse(rawURL, contentType string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "javascript") || strings.Contains(ct, "ecmascript") {
		return true
	}
	lower := strings.ToLower(rawURL)
	for _, suffix := range []string{".js", ".mjs", ".jsx"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// IsHTMLResponse 按 content-type 判断是否 HTML（无后缀回退）。
func IsHTMLResponse(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/html")
}

// IsTextResponse 判断响应体是否值得做密钥扫描：
// text/*、json、javascript、xml。content-type 缺失时按 JS 后缀放行（交给上层 URL 判断）。
func IsTextResponse(rawURL, contentType string) bool {
	ct := strings.ToLower(contentType)
	if ct == "" {
		return IsJSResponse(rawURL, "")
	}
	return strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "ecmascript") ||
		strings.Contains(ct, "xml")
}
