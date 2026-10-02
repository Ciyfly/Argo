package scope

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// 爬取范围判定与域外结果收集。
//
// 历史实现是三处 strings.Contains(url, host) 子串判断：
// 目标 a.com 会误匹配 http://evil-a.com/、http://x.a.com.evil.io/，
// 反过来想爬 dev.a.com 子域又进不来。这里统一收敛为精确的 host 匹配 +
// 可选的 include/exclude 正则，四个判断点（TabWork/PendUrlWork/hijack×2）全部走本包。

// defaultScope 是包级单例，Init 之后各判断点直接调用包函数。
var defaultScope *Scope

// Scope 保存目标站点的范围规则。
type Scope struct {
	// targetHostName 是目标主机名（不含端口），端口默认宽松：同 host 任意端口入域。
	targetHostName string
	crawlSubdomain bool
	includeRes     []*regexp.Regexp
	excludeRes     []*regexp.Regexp
}

// InitFromTarget 依据目标和配置构建范围规则。正则编译失败直接 Fatal，
// 让用户第一时间看到是哪条正则写错，而不是静默忽略后爬飞。
func InitFromTarget(target string, cfg conf.ScopeConf) {
	hostname := ""
	if u, err := url.Parse(target); err == nil {
		hostname = strings.ToLower(u.Hostname())
	}
	if hostname == "" {
		// 目标 URL 解析不出 host 时退回原串，至少不比之前的子串判断差
		hostname = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://"))
	}

	scopeObj := &Scope{
		targetHostName: hostname,
		crawlSubdomain: cfg.CrawlSubdomains,
	}
	for _, pattern := range cfg.Include {
		re, err := regexp.Compile(pattern)
		if err != nil {
			log.Logger.Fatalf("scope include regex invalid: %s err: %s", pattern, err)
		}
		scopeObj.includeRes = append(scopeObj.includeRes, re)
	}
	for _, pattern := range cfg.Exclude {
		re, err := regexp.Compile(pattern)
		if err != nil {
			log.Logger.Fatalf("scope exclude regex invalid: %s err: %s", pattern, err)
		}
		scopeObj.excludeRes = append(scopeObj.excludeRes, re)
	}
	defaultScope = scopeObj
}

// IsInScope 判断 URL 是否在爬取范围内。非法 URL 一律不在范围内。
//
// 判定顺序：exclude 命中 → 出域；include 非空且命中 → 入域；
// 默认 host 精确匹配（crawl_subdomains 开启时允许子域后缀）。
func IsInScope(rawURL string) bool {
	if defaultScope == nil {
		return true
	}
	return defaultScope.isInScope(rawURL)
}

func (s *Scope) isInScope(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return false
	}
	for _, re := range s.excludeRes {
		if re.MatchString(rawURL) {
			return false
		}
	}
	if len(s.includeRes) > 0 {
		for _, re := range s.includeRes {
			if re.MatchString(rawURL) {
				return true
			}
		}
		// 给了 include 却没命中：交给默认 host 规则兜底（include 用于扩域而非收紧）
	}
	host := strings.ToLower(u.Hostname())
	if s.crawlSubdomain && strings.HasSuffix(host, "."+s.targetHostName) {
		return true
	}
	return host == s.targetHostName
}

// ---- 域外结果收集：被 scope 丢掉的 URL 记录下来，供单独输出 ----

var (
	outScopeMu   sync.Mutex
	outScopeSeen map[string]bool
	outScopeList []string
)

// RecordOutScope 记录一个域外 URL，dedup 后保存。
func RecordOutScope(rawURL string) {
	if rawURL == "" {
		return
	}
	outScopeMu.Lock()
	defer outScopeMu.Unlock()
	if outScopeSeen == nil {
		outScopeSeen = make(map[string]bool)
	}
	if outScopeSeen[rawURL] {
		return
	}
	outScopeSeen[rawURL] = true
	outScopeList = append(outScopeList, rawURL)
}

// SnapshotOutScope 返回域外 URL 列表副本。
func SnapshotOutScope() []string {
	outScopeMu.Lock()
	defer outScopeMu.Unlock()
	out := make([]string, len(outScopeList))
	copy(out, outScopeList)
	return out
}

// ResetOutScope 每个目标开始前清空上一轮的域外记录。
func ResetOutScope() {
	outScopeMu.Lock()
	defer outScopeMu.Unlock()
	outScopeSeen = make(map[string]bool)
	outScopeList = nil
}
