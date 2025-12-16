//go:build cgo
// +build cgo

package static

import (
	"net/url"
	"strings"
	"sync"

	"argo/pkg/log"

	"github.com/BishopFox/jsluice"
)

// JSluiceAnalyzer 基于 jsluice 的 AST 级别 JS 分析器
// 相比正则匹配，能够识别：
// - fetch/axios/XMLHttpRequest 调用
// - jQuery AJAX 方法 ($.get, $.post, $.ajax)
// - 字符串拼接的 URL
// - Vue/React Router 定义
// - document.location 赋值
type JSluiceAnalyzer struct {
	mu sync.RWMutex
	// 配置
	config *JSluiceConfig
	// 统计
	urlsExtracted    int64
	secretsDetected  int64
	scriptsAnalyzed  int64
}

// JSluiceConfig 配置
type JSluiceConfig struct {
	// 是否启用敏感信息检测
	EnableSecretDetection bool
	// 最大脚本大小 (字节)
	MaxScriptSize int
	// 是否提取查询参数
	ExtractQueryParams bool
	// 是否提取 Body 参数
	ExtractBodyParams bool
	// 自定义 URL 匹配器
	CustomMatchers []JSluiceURLMatcher
}

// JSluiceURLMatcher 自定义 URL 匹配器
type JSluiceURLMatcher struct {
	Type        string // AST 节点类型: "string", "call_expression", "assignment_expression"
	MatchPrefix string // 匹配前缀，如 "mailto:", "ws:", "wss:"
	URLType     string // URL 类型标识
}

// JSluiceURL 提取的 URL 信息
type JSluiceURL struct {
	URL         string            `json:"url"`
	Method      string            `json:"method,omitempty"`
	Type        string            `json:"type"` // fetch, $.ajax, locationAssignment, etc.
	QueryParams []string          `json:"query_params,omitempty"`
	BodyParams  []string          `json:"body_params,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Source      string            `json:"source,omitempty"`
}

// JSluiceSecret 检测到的敏感信息
type JSluiceSecret struct {
	Kind     string      `json:"kind"`     // AWSAccessKey, gcpKey, githubKey, etc.
	Data     interface{} `json:"data"`     // 敏感数据 (已脱敏)
	Severity string      `json:"severity"` // low, medium, high
	Context  string      `json:"context,omitempty"`
}

// JSluiceResult 分析结果
type JSluiceResult struct {
	URLs    []*JSluiceURL    `json:"urls"`
	Secrets []*JSluiceSecret `json:"secrets,omitempty"`
	// API 端点 (从 URLs 中提取的 API 类型)
	APIEndpoints []*JSluiceURL `json:"api_endpoints,omitempty"`
	// 统计
	TotalURLs     int `json:"total_urls"`
	TotalSecrets  int `json:"total_secrets"`
	TotalAPIs     int `json:"total_apis"`
}

// DefaultJSluiceConfig 默认配置
func DefaultJSluiceConfig() *JSluiceConfig {
	return &JSluiceConfig{
		EnableSecretDetection: true,
		MaxScriptSize:         10 * 1024 * 1024, // 10MB
		ExtractQueryParams:    true,
		ExtractBodyParams:     true,
		CustomMatchers: []JSluiceURLMatcher{
			// WebSocket URLs
			{Type: "string", MatchPrefix: "ws:", URLType: "websocket"},
			{Type: "string", MatchPrefix: "wss:", URLType: "websocket"},
		},
	}
}

// NewJSluiceAnalyzer 创建 JSluice 分析器
func NewJSluiceAnalyzer(config *JSluiceConfig) *JSluiceAnalyzer {
	if config == nil {
		config = DefaultJSluiceConfig()
	}
	return &JSluiceAnalyzer{
		config: config,
	}
}

// AnalyzeJS 分析 JavaScript 代码
func (ja *JSluiceAnalyzer) AnalyzeJS(jsContent string) *JSluiceResult {
	if jsContent == "" {
		return &JSluiceResult{}
	}

	// 检查大小限制
	if len(jsContent) > ja.config.MaxScriptSize {
		log.Logger.Debugf("JSluice: script too large (%d bytes), truncating", len(jsContent))
		jsContent = jsContent[:ja.config.MaxScriptSize]
	}

	ja.mu.Lock()
	ja.scriptsAnalyzed++
	ja.mu.Unlock()

	result := &JSluiceResult{
		URLs:         make([]*JSluiceURL, 0),
		Secrets:      make([]*JSluiceSecret, 0),
		APIEndpoints: make([]*JSluiceURL, 0),
	}

	// 创建 jsluice 分析器
	analyzer := jsluice.NewAnalyzer([]byte(jsContent))

	// 添加自定义匹配器
	for _, matcher := range ja.config.CustomMatchers {
		ja.addCustomMatcher(analyzer, matcher)
	}

	// 提取 URLs
	urls := analyzer.GetURLs()
	for _, u := range urls {
		jsURL := &JSluiceURL{
			URL:         u.URL,
			Method:      u.Method,
			Type:        u.Type,
			QueryParams: u.QueryParams,
			BodyParams:  u.BodyParams,
			Headers:     u.Headers,
			ContentType: u.ContentType,
			Source:      u.Source,
		}

		// 过滤无效 URL
		if !ja.isValidURL(jsURL.URL) {
			continue
		}

		result.URLs = append(result.URLs, jsURL)

		// 判断是否为 API 端点
		if ja.isAPIEndpoint(jsURL) {
			result.APIEndpoints = append(result.APIEndpoints, jsURL)
		}

		ja.mu.Lock()
		ja.urlsExtracted++
		ja.mu.Unlock()
	}

	// 检测敏感信息
	if ja.config.EnableSecretDetection {
		secrets := analyzer.GetSecrets()
		for _, s := range secrets {
			secret := &JSluiceSecret{
				Kind:     s.Kind,
				Data:     ja.maskSecretData(s.Data),
				Severity: string(s.Severity),
			}
			if s.Context != nil {
				secret.Context = truncateString(formatContext(s.Context), 100)
			}
			result.Secrets = append(result.Secrets, secret)

			ja.mu.Lock()
			ja.secretsDetected++
			ja.mu.Unlock()
		}
	}

	// 更新统计
	result.TotalURLs = len(result.URLs)
	result.TotalSecrets = len(result.Secrets)
	result.TotalAPIs = len(result.APIEndpoints)

	return result
}

// AnalyzeJSWithBaseURL 分析 JS 并解析相对 URL
func (ja *JSluiceAnalyzer) AnalyzeJSWithBaseURL(jsContent, baseURL string) *JSluiceResult {
	result := ja.AnalyzeJS(jsContent)

	// 解析相对 URL
	base, err := url.Parse(baseURL)
	if err != nil {
		return result
	}

	for _, u := range result.URLs {
		resolved := ja.resolveURL(u.URL, base)
		if resolved != "" {
			u.URL = resolved
		}
	}

	for _, api := range result.APIEndpoints {
		resolved := ja.resolveURL(api.URL, base)
		if resolved != "" {
			api.URL = resolved
		}
	}

	return result
}

// GetURLStrings 获取所有 URL 字符串列表
func (ja *JSluiceAnalyzer) GetURLStrings(jsContent string) []string {
	result := ja.AnalyzeJS(jsContent)
	urls := make([]string, 0, len(result.URLs))
	seen := make(map[string]bool)

	for _, u := range result.URLs {
		if !seen[u.URL] {
			urls = append(urls, u.URL)
			seen[u.URL] = true
		}
	}

	return urls
}

// GetURLStringsWithBase 获取所有 URL 字符串列表（解析相对 URL）
func (ja *JSluiceAnalyzer) GetURLStringsWithBase(jsContent, baseURL string) []string {
	result := ja.AnalyzeJSWithBaseURL(jsContent, baseURL)
	urls := make([]string, 0, len(result.URLs))
	seen := make(map[string]bool)

	for _, u := range result.URLs {
		if !seen[u.URL] {
			urls = append(urls, u.URL)
			seen[u.URL] = true
		}
	}

	return urls
}

// GetAPIEndpoints 仅获取 API 端点
func (ja *JSluiceAnalyzer) GetAPIEndpoints(jsContent string) []*JSluiceURL {
	result := ja.AnalyzeJS(jsContent)
	return result.APIEndpoints
}

// addCustomMatcher 添加自定义匹配器
func (ja *JSluiceAnalyzer) addCustomMatcher(analyzer *jsluice.Analyzer, matcher JSluiceURLMatcher) {
	analyzer.AddURLMatcher(jsluice.URLMatcher{
		Type: matcher.Type,
		Fn: func(n *jsluice.Node) *jsluice.URL {
			val := n.DecodedString()
			if matcher.MatchPrefix != "" && !strings.HasPrefix(val, matcher.MatchPrefix) {
				return nil
			}
			return &jsluice.URL{
				URL:  val,
				Type: matcher.URLType,
			}
		},
	})
}

// isValidURL 验证 URL 是否有效
func (ja *JSluiceAnalyzer) isValidURL(urlStr string) bool {
	if urlStr == "" || len(urlStr) < 2 || len(urlStr) > 2000 {
		return false
	}

	// 跳过特殊协议和无效值
	lower := strings.ToLower(urlStr)
	invalidPrefixes := []string{
		"javascript:", "data:", "mailto:", "tel:", "blob:",
		"chrome:", "chrome-extension:", "moz-extension:",
		"about:", "file:",
	}
	for _, prefix := range invalidPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}

	// 跳过模板字符串
	if strings.Contains(urlStr, "${") || strings.Contains(urlStr, "{{") {
		return false
	}

	// 跳过纯变量引用
	if strings.HasPrefix(urlStr, "$") || strings.HasPrefix(urlStr, "@") {
		return false
	}

	// 跳过仅包含占位符的 URL
	if urlStr == "#" || urlStr == "/" || urlStr == "./" || urlStr == "../" {
		return false
	}

	// 经验性过滤：要求具备“URL/路径”特征，避免把 viewport/X-UA-Compatible 等配置串误当成 URL。
	// 允许：绝对 URL、协议相对、根相对、相对路径(含 /)、文件/域名(含 .)、查询(含 ?)
	if !looksLikeURLish(urlStr) {
		return false
	}

	return true
}

func looksLikeURLish(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "//") {
		return true
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "../") {
		return true
	}
	// 只要包含路径/文件/查询特征，就认为“可能是 URL”
	if strings.ContainsAny(trimmed, "/.?") || strings.Contains(trimmed, "?") {
		return true
	}
	return false
}

// isAPIEndpoint 判断是否为 API 端点
func (ja *JSluiceAnalyzer) isAPIEndpoint(u *JSluiceURL) bool {
	// 根据 Type 判断
	apiTypes := map[string]bool{
		"fetch":       true,
		"$.ajax":      true,
		"$.get":       true,
		"$.post":      true,
		"$.put":       true,
		"$.delete":    true,
		"$.getJSON":   true,
		"axios":       true,
		"xhr":         true,
		"XMLHttpRequest": true,
	}
	if apiTypes[u.Type] {
		return true
	}

	// 根据 URL 路径判断
	urlLower := strings.ToLower(u.URL)
	apiPatterns := []string{
		"/api/", "/api.", "/rest/", "/graphql",
		"/v1/", "/v2/", "/v3/",
		".json", ".xml",
		"/ajax/", "/rpc/",
	}
	for _, pattern := range apiPatterns {
		if strings.Contains(urlLower, pattern) {
			return true
		}
	}

	// 有 HTTP 方法的通常是 API
	if u.Method != "" && u.Method != "GET" {
		return true
	}

	// 有请求头或 Body 参数的通常是 API
	if len(u.Headers) > 0 || len(u.BodyParams) > 0 {
		return true
	}

	return false
}

// resolveURL 解析相对 URL
func (ja *JSluiceAnalyzer) resolveURL(urlStr string, base *url.URL) string {
	if urlStr == "" || base == nil {
		return urlStr
	}

	// 已经是绝对 URL
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		return urlStr
	}

	// 协议相对 URL
	if strings.HasPrefix(urlStr, "//") {
		return base.Scheme + ":" + urlStr
	}

	// 解析相对 URL
	ref, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}

	resolved := base.ResolveReference(ref)
	return resolved.String()
}

// maskSecretData 脱敏敏感数据
func (ja *JSluiceAnalyzer) maskSecretData(data interface{}) interface{} {
	switch v := data.(type) {
	case string:
		return maskString(v)
	case map[string]interface{}:
		masked := make(map[string]interface{})
		for k, val := range v {
			if strVal, ok := val.(string); ok {
				masked[k] = maskString(strVal)
			} else {
				masked[k] = val
			}
		}
		return masked
	default:
		return "[REDACTED]"
	}
}

// maskString 脱敏字符串
func maskString(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

// truncateString 截断字符串
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatContext 格式化上下文
func formatContext(ctx interface{}) string {
	switch v := ctx.(type) {
	case string:
		return v
	case map[string]interface{}:
		var parts []string
		for k, val := range v {
			parts = append(parts, k+"="+formatContext(val))
		}
		return strings.Join(parts, ", ")
	default:
		return ""
	}
}

// Stats 获取统计信息
func (ja *JSluiceAnalyzer) Stats() JSluiceStats {
	ja.mu.RLock()
	defer ja.mu.RUnlock()
	return JSluiceStats{
		URLsExtracted:   ja.urlsExtracted,
		SecretsDetected: ja.secretsDetected,
		ScriptsAnalyzed: ja.scriptsAnalyzed,
	}
}

// JSluiceStats 统计信息
type JSluiceStats struct {
	URLsExtracted   int64 `json:"urls_extracted"`
	SecretsDetected int64 `json:"secrets_detected"`
	ScriptsAnalyzed int64 `json:"scripts_analyzed"`
}

// 全局单例
var (
	globalJSluiceAnalyzer *JSluiceAnalyzer
	jsluiceOnce           sync.Once
)

// GetJSluiceAnalyzer 获取全局 JSluice 分析器
func GetJSluiceAnalyzer() *JSluiceAnalyzer {
	jsluiceOnce.Do(func() {
		globalJSluiceAnalyzer = NewJSluiceAnalyzer(nil)
	})
	return globalJSluiceAnalyzer
}

// ParseJSWithJSluice 使用 JSluice 解析 JS 内容
// 这是供外部调用的便捷函数
func ParseJSWithJSluice(jsContent, baseURL string) []string {
	analyzer := GetJSluiceAnalyzer()
	if baseURL != "" {
		return analyzer.GetURLStringsWithBase(jsContent, baseURL)
	}
	return analyzer.GetURLStrings(jsContent)
}

// ParseJSForAPIs 使用 JSluice 提取 API 端点
func ParseJSForAPIs(jsContent string) []*JSluiceURL {
	analyzer := GetJSluiceAnalyzer()
	return analyzer.GetAPIEndpoints(jsContent)
}
