//go:build !cgo
// +build !cgo

package static

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// JSluiceAnalyzer 在无 cgo 环境下的降级版本。
// 仅提供“字符串/正则”级别的 URL 抽取能力，用于保证多平台可构建。
type JSluiceAnalyzer struct {
	config *JSluiceConfig
}

// JSluiceConfig 配置（保持与 cgo 版本字段兼容）
type JSluiceConfig struct {
	EnableSecretDetection bool
	MaxScriptSize         int
	ExtractQueryParams    bool
	ExtractBodyParams     bool
	CustomMatchers        []JSluiceURLMatcher
}

// JSluiceURLMatcher 自定义 URL 匹配器（无 cgo 版本不做 AST 匹配，仅保留结构体用于兼容）
type JSluiceURLMatcher struct {
	Type        string
	MatchPrefix string
	URLType     string
}

// 在无 cgo 环境下（例如交叉编译 windows/linux-arm64）无法使用基于 tree-sitter 的 JSluice，
// 这里提供一个降级实现：通过轻量正则从 JS 字符串字面量中提取 URL/路径。
//
// 说明：
// - 优先覆盖 http(s)://、//cdn、以及以 / ./ ../ 开头的路径（常见 fetch/xhr/axios/router 写法）。
// - 不做 AST 级别语义分析，准确率/覆盖面低于 JSluice，但可保证多平台可构建。
func ParseJSWithJSluice(jsContent, baseURL string) []string {
	if jsContent == "" {
		return nil
	}

	candidates := make([]string, 0, 64)
	// 复用原有正则：https?:// / www / domain.tld/path
	candidates = append(candidates, findUrlMatch(jsContent)...)
	// 补充：协议相对、根相对、相对路径（来自字符串字面量）
	candidates = append(candidates, extractJSStringURLCandidates(jsContent)...)

	candidates = dedupeStrings(candidates)
	if baseURL == "" {
		// baseURL 为空时不做 canonicalize，避免把相对路径直接丢弃；上层会在有 base 的地方再 Resolve。
		return candidates
	}
	return HandlerUrls(candidates, baseURL)
}

// DefaultJSluiceConfig 默认配置（无 cgo 版本会被用于初始化占位分析器）
func DefaultJSluiceConfig() *JSluiceConfig {
	return &JSluiceConfig{
		EnableSecretDetection: false,
		MaxScriptSize:         10 * 1024 * 1024,
		ExtractQueryParams:    true,
		ExtractBodyParams:     true,
		CustomMatchers: []JSluiceURLMatcher{
			{Type: "string", MatchPrefix: "ws:", URLType: "websocket"},
			{Type: "string", MatchPrefix: "wss:", URLType: "websocket"},
		},
	}
}

// NewJSluiceAnalyzer 创建 JSluice 分析器（无 cgo 版本为降级实现）
func NewJSluiceAnalyzer(config *JSluiceConfig) *JSluiceAnalyzer {
	if config == nil {
		config = DefaultJSluiceConfig()
	}
	return &JSluiceAnalyzer{config: config}
}

// JSluiceURL 提取的 URL 信息（保持与 cgo 版本字段兼容，供引擎统一处理）
type JSluiceURL struct {
	URL         string            `json:"url"`
	Method      string            `json:"method,omitempty"`
	Type        string            `json:"type"`
	QueryParams []string          `json:"query_params,omitempty"`
	BodyParams  []string          `json:"body_params,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Source      string            `json:"source,omitempty"`
}

// JSluiceSecret 检测到的敏感信息（无 cgo 版本不做真实检测，仅保留结构体）
type JSluiceSecret struct {
	Kind     string      `json:"kind"`
	Data     interface{} `json:"data"`
	Severity string      `json:"severity"`
	Context  string      `json:"context,omitempty"`
}

// JSluiceResult 分析结果（保持与 cgo 版本字段兼容）
type JSluiceResult struct {
	URLs         []*JSluiceURL    `json:"urls"`
	Secrets      []*JSluiceSecret `json:"secrets,omitempty"`
	APIEndpoints []*JSluiceURL    `json:"api_endpoints,omitempty"`
	TotalURLs    int              `json:"total_urls"`
	TotalSecrets int              `json:"total_secrets"`
	TotalAPIs    int              `json:"total_apis"`
}

// AnalyzeJS 降级版分析：仅基于字符串/正则提取 URL，供引擎统一处理。
func (ja *JSluiceAnalyzer) AnalyzeJS(jsContent string) *JSluiceResult {
	if jsContent == "" {
		return &JSluiceResult{}
	}
	if ja != nil && ja.config != nil && ja.config.MaxScriptSize > 0 && len(jsContent) > ja.config.MaxScriptSize {
		jsContent = jsContent[:ja.config.MaxScriptSize]
	}

	raw := ParseJSWithJSluice(jsContent, "")
	result := &JSluiceResult{
		URLs:         make([]*JSluiceURL, 0, len(raw)),
		Secrets:      make([]*JSluiceSecret, 0),
		APIEndpoints: make([]*JSluiceURL, 0),
	}

	for _, u := range raw {
		if u == "" {
			continue
		}
		item := &JSluiceURL{
			URL:         u,
			Type:        classifyFallbackURLType(u),
			QueryParams: extractQueryParamKeys(u),
			Headers:     nil,
			BodyParams:  nil,
			Source:      "nocgo_regex",
		}
		result.URLs = append(result.URLs, item)
		if isLikelyAPIEndpoint(u) {
			// 复用同一个 item 指针即可
			result.APIEndpoints = append(result.APIEndpoints, item)
		}
	}

	result.TotalURLs = len(result.URLs)
	result.TotalSecrets = len(result.Secrets)
	result.TotalAPIs = len(result.APIEndpoints)
	return result
}

// AnalyzeJSWithBaseURL 降级版：仅对 URL 做 base 解析（不做 AST）。
func (ja *JSluiceAnalyzer) AnalyzeJSWithBaseURL(jsContent, baseURL string) *JSluiceResult {
	result := ja.AnalyzeJS(jsContent)
	if result == nil || baseURL == "" {
		return result
	}
	for _, u := range result.URLs {
		if u == nil {
			continue
		}
		if resolved := HandlerUrl(u.URL, baseURL); resolved != "" {
			u.URL = resolved
		}
	}
	for _, api := range result.APIEndpoints {
		if api == nil {
			continue
		}
		if resolved := HandlerUrl(api.URL, baseURL); resolved != "" {
			api.URL = resolved
		}
	}
	return result
}

// GetURLStrings 获取所有 URL 字符串列表
func (ja *JSluiceAnalyzer) GetURLStrings(jsContent string) []string {
	result := ja.AnalyzeJS(jsContent)
	if result == nil || len(result.URLs) == 0 {
		return nil
	}
	out := make([]string, 0, len(result.URLs))
	seen := make(map[string]struct{}, len(result.URLs))
	for _, u := range result.URLs {
		if u == nil || u.URL == "" {
			continue
		}
		if _, ok := seen[u.URL]; ok {
			continue
		}
		seen[u.URL] = struct{}{}
		out = append(out, u.URL)
	}
	return out
}

// GetURLStringsWithBase 获取所有 URL 字符串列表（解析相对 URL）
func (ja *JSluiceAnalyzer) GetURLStringsWithBase(jsContent, baseURL string) []string {
	if baseURL == "" {
		return ja.GetURLStrings(jsContent)
	}
	raw := ParseJSWithJSluice(jsContent, "")
	return HandlerUrls(raw, baseURL)
}

// GetAPIEndpoints 仅获取 API 端点（降级版：基于 URL 规则判断）
func (ja *JSluiceAnalyzer) GetAPIEndpoints(jsContent string) []*JSluiceURL {
	result := ja.AnalyzeJS(jsContent)
	if result == nil {
		return nil
	}
	return result.APIEndpoints
}

// ParseJSForAPIs 使用降级版提取 API 端点
func ParseJSForAPIs(jsContent string) []*JSluiceURL {
	analyzer := GetJSluiceAnalyzer()
	return analyzer.GetAPIEndpoints(jsContent)
}

// 全局单例（与 cgo 版本保持一致的入口）
var (
	globalJSluiceAnalyzer *JSluiceAnalyzer
	jsluiceOnce           sync.Once
)

func GetJSluiceAnalyzer() *JSluiceAnalyzer {
	jsluiceOnce.Do(func() {
		globalJSluiceAnalyzer = NewJSluiceAnalyzer(nil)
	})
	return globalJSluiceAnalyzer
}

var jsStringURLRe = regexp.MustCompile(`(?is)(?:'|")((?:https?://|wss?://|//|/|\.\.?/)[^'"\\\s]+)(?:'|")`)

func extractJSStringURLCandidates(content string) []string {
	matches := jsStringURLRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		out = append(out, m[1])
	}
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func classifyFallbackURLType(u string) string {
	lower := strings.ToLower(strings.TrimSpace(u))
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") {
		return "websocket"
	}
	if isLikelyAPIEndpoint(lower) {
		// 引擎对 "fetch" 识别为 API，这里用作降级场景的近似分类
		return "fetch"
	}
	return "string"
}

func isLikelyAPIEndpoint(u string) bool {
	lower := strings.ToLower(u)
	patterns := []string{
		"/api/", "/api.", "/rest/", "/graphql",
		"/v1/", "/v2/", "/v3/",
		".json", ".xml",
		"/ajax/", "/rpc/",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func extractQueryParamKeys(u string) []string {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil
	}
	q := parsed.Query()
	if len(q) == 0 {
		return nil
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
