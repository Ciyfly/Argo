// Package engine provides scope control for crawling
// P4优化: 作用域控制 - 精确控制爬取范围
package engine

import (
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// ScopeConfig 作用域配置
type ScopeConfig struct {
	// 包含规则
	IncludeDomains    []string `yaml:"include_domains" json:"include_domains"`       // 包含的域名
	IncludeSubdomains bool     `yaml:"include_subdomains" json:"include_subdomains"` // 包含子域名
	IncludePaths      []string `yaml:"include_paths" json:"include_paths"`           // 包含的路径前缀
	IncludePatterns   []string `yaml:"include_patterns" json:"include_patterns"`     // 包含的正则模式

	// 排除规则
	ExcludeDomains    []string `yaml:"exclude_domains" json:"exclude_domains"`       // 排除的域名
	ExcludePaths      []string `yaml:"exclude_paths" json:"exclude_paths"`           // 排除的路径前缀
	ExcludePatterns   []string `yaml:"exclude_patterns" json:"exclude_patterns"`     // 排除的正则模式
	ExcludeExtensions []string `yaml:"exclude_extensions" json:"exclude_extensions"` // 排除的扩展名

	// 特殊排除
	ExcludeCDN       bool `yaml:"exclude_cdn" json:"exclude_cdn"`               // 排除 CDN 域名
	ExcludePrivateIP bool `yaml:"exclude_private_ip" json:"exclude_private_ip"` // 排除私有 IP
	ExcludeExternal  bool `yaml:"exclude_external" json:"exclude_external"`     // 排除外部链接

	// 深度控制
	MaxDepth int `yaml:"max_depth" json:"max_depth"` // 最大深度
}

// DefaultScopeConfig 默认作用域配置
func DefaultScopeConfig() *ScopeConfig {
	return &ScopeConfig{
		IncludeSubdomains: true,
		ExcludeCDN:        true,
		ExcludePrivateIP:  false,
		ExcludeExternal:   true,
		MaxDepth:          10,
		ExcludeExtensions: []string{
			".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".bmp",
			".woff", ".woff2", ".ttf", ".eot", ".otf",
			".mp4", ".mp3", ".avi", ".mov", ".wmv", ".flv", ".webm",
			".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
			".zip", ".rar", ".7z", ".tar", ".gz", ".bz2",
		},
		ExcludePaths: []string{
			"/wp-content/uploads/",
			"/wp-includes/",
			"/static/images/",
			"/assets/images/",
			"/node_modules/",
		},
	}
}

// ScopeController 作用域控制器
type ScopeController struct {
	config         *ScopeConfig
	targetDomain   string
	includeRegexes []*regexp.Regexp
	excludeRegexes []*regexp.Regexp
	cdnDomains     map[string]bool
	mu             sync.RWMutex
	stats          ScopeStats
}

// ScopeStats 作用域统计
type ScopeStats struct {
	TotalChecked    int `json:"total_checked"`
	Allowed         int `json:"allowed"`
	BlockedDomain   int `json:"blocked_domain"`
	BlockedPath     int `json:"blocked_path"`
	BlockedExt      int `json:"blocked_ext"`
	BlockedCDN      int `json:"blocked_cdn"`
	BlockedPrivate  int `json:"blocked_private"`
	BlockedExternal int `json:"blocked_external"`
	BlockedDepth    int `json:"blocked_depth"`
	BlockedPattern  int `json:"blocked_pattern"`
}

// 常见 CDN 域名
var commonCDNDomains = []string{
	"cloudflare.com", "cloudflare-dns.com",
	"akamai.net", "akamaiedge.net", "akamaihd.net",
	"fastly.net", "fastlylb.net",
	"cloudfront.net", "amazonaws.com",
	"azureedge.net", "azure.com",
	"googleusercontent.com", "googleapis.com", "gstatic.com",
	"cdnjs.cloudflare.com", "cdn.jsdelivr.net", "unpkg.com",
	"bootstrapcdn.com", "fontawesome.com",
	"jquery.com", "code.jquery.com",
	"googletagmanager.com", "google-analytics.com", "doubleclick.net",
	"facebook.net", "fbcdn.net", "facebook.com",
	"twitter.com", "twimg.com",
	"linkedin.com", "licdn.com",
	"gravatar.com", "wp.com",
	"recaptcha.net", "gstatic.com",
}

// NewScopeController 创建作用域控制器
func NewScopeController(targetURL string, config *ScopeConfig) *ScopeController {
	if config == nil {
		config = DefaultScopeConfig()
	}

	// 解析目标域名
	targetDomain := ""
	if u, err := url.Parse(targetURL); err == nil {
		targetDomain = u.Hostname()
	}

	sc := &ScopeController{
		config:       config,
		targetDomain: targetDomain,
		cdnDomains:   make(map[string]bool),
	}

	// 初始化 CDN 域名集合
	for _, domain := range commonCDNDomains {
		sc.cdnDomains[domain] = true
	}

	// 编译正则表达式
	for _, pattern := range config.IncludePatterns {
		if re, err := regexp.Compile(pattern); err == nil {
			sc.includeRegexes = append(sc.includeRegexes, re)
		}
	}

	for _, pattern := range config.ExcludePatterns {
		if re, err := regexp.Compile(pattern); err == nil {
			sc.excludeRegexes = append(sc.excludeRegexes, re)
		}
	}

	return sc
}

// IsInScope 检查 URL 是否在作用域内
func (sc *ScopeController) IsInScope(urlStr string, depth int) (bool, string) {
	sc.mu.Lock()
	sc.stats.TotalChecked++
	sc.mu.Unlock()

	u, err := url.Parse(urlStr)
	if err != nil {
		return false, "invalid_url"
	}

	hostname := u.Hostname()
	path := u.Path

	// 1. 检查深度
	if sc.config.MaxDepth > 0 && depth > sc.config.MaxDepth {
		sc.incrementStat("blocked_depth")
		return false, "max_depth_exceeded"
	}

	// 2. 检查扩展名
	if sc.isExcludedExtension(path) {
		sc.incrementStat("blocked_ext")
		return false, "excluded_extension"
	}

	// 3. 检查私有 IP
	if sc.config.ExcludePrivateIP && sc.isPrivateIP(hostname) {
		sc.incrementStat("blocked_private")
		return false, "private_ip"
	}

	// 4. 检查 CDN
	if sc.config.ExcludeCDN && sc.isCDNDomain(hostname) {
		sc.incrementStat("blocked_cdn")
		return false, "cdn_domain"
	}

	// 5. 检查域名
	if !sc.isDomainAllowed(hostname) {
		if sc.config.ExcludeExternal {
			sc.incrementStat("blocked_external")
			return false, "external_domain"
		}
		sc.incrementStat("blocked_domain")
		return false, "excluded_domain"
	}

	// 6. 检查路径排除
	if sc.isExcludedPath(path) {
		sc.incrementStat("blocked_path")
		return false, "excluded_path"
	}

	// 7. 检查正则排除
	if sc.matchesExcludePattern(urlStr) {
		sc.incrementStat("blocked_pattern")
		return false, "excluded_pattern"
	}

	// 8. 如果有包含规则，检查是否匹配
	if len(sc.config.IncludePaths) > 0 || len(sc.includeRegexes) > 0 {
		if !sc.matchesIncludeRules(urlStr, path) {
			sc.incrementStat("blocked_pattern")
			return false, "not_in_include_rules"
		}
	}

	sc.incrementStat("allowed")
	return true, ""
}

// isDomainAllowed 检查域名是否允许
func (sc *ScopeController) isDomainAllowed(hostname string) bool {
	// 检查排除域名
	for _, domain := range sc.config.ExcludeDomains {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return false
		}
	}

	// 如果有包含域名规则
	if len(sc.config.IncludeDomains) > 0 {
		for _, domain := range sc.config.IncludeDomains {
			if hostname == domain {
				return true
			}
			if sc.config.IncludeSubdomains && strings.HasSuffix(hostname, "."+domain) {
				return true
			}
		}
		return false
	}

	// 检查是否是目标域名或其子域名
	if sc.targetDomain != "" {
		if hostname == sc.targetDomain {
			return true
		}
		if sc.config.IncludeSubdomains && strings.HasSuffix(hostname, "."+sc.targetDomain) {
			return true
		}
		// 外部域名，根据配置决定
		return !sc.config.ExcludeExternal
	}

	return true
}

// isExcludedExtension 检查是否是排除的扩展名
func (sc *ScopeController) isExcludedExtension(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range sc.config.ExcludeExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// isExcludedPath 检查是否是排除的路径
func (sc *ScopeController) isExcludedPath(path string) bool {
	lower := strings.ToLower(path)
	for _, excludePath := range sc.config.ExcludePaths {
		if strings.Contains(lower, strings.ToLower(excludePath)) {
			return true
		}
	}
	return false
}

// isPrivateIP 检查是否是私有 IP
func (sc *ScopeController) isPrivateIP(hostname string) bool {
	ip := net.ParseIP(hostname)
	if ip == nil {
		// 尝试解析域名
		ips, err := net.LookupIP(hostname)
		if err != nil || len(ips) == 0 {
			return false
		}
		ip = ips[0]
	}

	// 检查私有 IP 范围
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}

	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// isCDNDomain 检查是否是 CDN 域名
func (sc *ScopeController) isCDNDomain(hostname string) bool {
	lower := strings.ToLower(hostname)

	// 直接匹配
	if sc.cdnDomains[lower] {
		return true
	}

	// 检查后缀
	for domain := range sc.cdnDomains {
		if strings.HasSuffix(lower, "."+domain) || lower == domain {
			return true
		}
	}

	return false
}

// matchesExcludePattern 检查是否匹配排除模式
func (sc *ScopeController) matchesExcludePattern(urlStr string) bool {
	for _, re := range sc.excludeRegexes {
		if re.MatchString(urlStr) {
			return true
		}
	}
	return false
}

// matchesIncludeRules 检查是否匹配包含规则
func (sc *ScopeController) matchesIncludeRules(urlStr string, path string) bool {
	// 检查路径前缀
	for _, includePath := range sc.config.IncludePaths {
		if strings.HasPrefix(path, includePath) {
			return true
		}
	}

	// 检查正则
	for _, re := range sc.includeRegexes {
		if re.MatchString(urlStr) {
			return true
		}
	}

	return false
}

// incrementStat 增加统计
func (sc *ScopeController) incrementStat(stat string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	switch stat {
	case "allowed":
		sc.stats.Allowed++
	case "blocked_domain":
		sc.stats.BlockedDomain++
	case "blocked_path":
		sc.stats.BlockedPath++
	case "blocked_ext":
		sc.stats.BlockedExt++
	case "blocked_cdn":
		sc.stats.BlockedCDN++
	case "blocked_private":
		sc.stats.BlockedPrivate++
	case "blocked_external":
		sc.stats.BlockedExternal++
	case "blocked_depth":
		sc.stats.BlockedDepth++
	case "blocked_pattern":
		sc.stats.BlockedPattern++
	}
}

// GetStats 获取统计信息
func (sc *ScopeController) GetStats() ScopeStats {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.stats
}

// AddExcludeDomain 添加排除域名
func (sc *ScopeController) AddExcludeDomain(domain string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.config.ExcludeDomains = append(sc.config.ExcludeDomains, domain)
}

// AddIncludeDomain 添加包含域名
func (sc *ScopeController) AddIncludeDomain(domain string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.config.IncludeDomains = append(sc.config.IncludeDomains, domain)
}

// AddExcludePattern 添加排除模式
func (sc *ScopeController) AddExcludePattern(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.excludeRegexes = append(sc.excludeRegexes, re)
	sc.config.ExcludePatterns = append(sc.config.ExcludePatterns, pattern)
	return nil
}

// SetMaxDepth 设置最大深度
func (sc *ScopeController) SetMaxDepth(depth int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.config.MaxDepth = depth
}
