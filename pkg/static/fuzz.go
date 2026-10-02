package static

import (
	"argo/pkg/log"
	"argo/pkg/ratelimit"
	"argo/pkg/req"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// 常见路径探测（Go 侧 http，不开浏览器）。
//
// 对标 crawlergo 的 path fuzz：字典并发探测，2xx 或 301/302 同 host 判有效，
// 有效路径入爬取队列走现有管线（文档类开 tab、接口类 record-only 自动分流）。
// 默认关闭（--fuzz）：这是主动探测行为，需用户显式开启。

// fuzzConcurrency 并发探测数（与 crawlergo 同量级）
const fuzzConcurrency = 20

// fuzzRequestTimeout 单条探测超时
const fuzzRequestTimeout = 2 * time.Second

// builtInFuzzPaths 内置常见路径字典：通用目录/入口词，不含任何站点特定项。
var builtInFuzzPaths = []string{
	"admin", "admin/login", "administrator", "manage", "manager", "backend", "console",
	"login", "signin", "register", "signup", "user", "users", "member", "members",
	"api", "api/v1", "api/v2", "graphql", "rest", "rpc", "soap", "ws",
	"backup", "bak", "old", "new", "test", "dev", "demo", "stage", " staging",
	"upload", "uploads", "file", "files", "download", "downloads", "attachment", "attachments",
	"data", "database", "db", "sql", "export", "import", "dump",
	"config", "conf", "configs", "settings", "env", "ini",
	"static", "assets", "public", "resource", "resources", "media", "images", "img",
	"js", "css", "dist", "build", "lib", "libs", "vendor", "node_modules",
	"doc", "docs", "help", "wiki", "manual", "guide", "faq",
	"report", "reports", "stat", "stats", "statistics", "monitor", "dashboard",
	"order", "orders", "pay", "payment", "cart", "checkout", "shop", "product", "products", "goods",
	"article", "articles", "news", "post", "posts", "blog", "comment", "comments",
	"search", "query", "list", "detail", "view", "show", "info", "profile",
	"auth", "oauth", "sso", "token", "session", "captcha", "verify",
	"cron", "job", "jobs", "task", "tasks", "queue", "worker",
	"log", "logs", "temp", "tmp", "cache", "session", "runtime",
	"install", "setup", "init", "upgrade", "migrate",
	"tools", "tool", "util", "utils", "scripts", "script", "cgi-bin",
	"git", ".git", ".svn", ".hg", ".DS_Store", ".env", ".idea",
	"phpinfo.php", "info.php", "test.php", "index.bak", "backup.sql", "dump.sql",
	"robots.txt", "sitemap.xml", "crossdomain.xml", "clientaccesspolicy.xml",
	"swagger", "swagger-ui", "api-docs", "doc.json", "openapi.json", "graphql/playground",
}

// LoadFuzzPaths 返回探测字典：外部文件优先（每行一个路径），否则内置字典。
// 外部文件清洗：去空白、去前导斜杠、跳过空行与注释行。
func LoadFuzzPaths(dictPath string) []string {
	if dictPath == "" {
		return builtInFuzzPaths
	}
	data, err := os.ReadFile(dictPath)
	if err != nil {
		log.Logger.Errorf("fuzz dict read err: %s, fallback to built-in", err)
		return builtInFuzzPaths
	}
	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		paths = append(paths, strings.TrimPrefix(line, "/"))
	}
	if len(paths) == 0 {
		log.Logger.Warnf("fuzz dict %s 为空，回退内置字典", dictPath)
		return builtInFuzzPaths
	}
	return paths
}

// FuzzPaths 对目标 host 并发探测字典路径，返回有效 URL（2xx 或 301/302 同 host）。
// onValid 对每条有效 URL 回调（由调用方入爬取队列），探测本身并发受 fuzzConcurrency 限制。
func FuzzPaths(ctx context.Context, target string, dictPath string, onValid func(foundURL string)) {
	base, err := url.Parse(target)
	if err != nil || base.Host == "" {
		log.Logger.Errorf("fuzz target invalid: %s", target)
		return
	}
	paths := LoadFuzzPaths(dictPath)
	log.Logger.Infof("[   fuzz   ] 开始探测 %d 条常见路径", len(paths))

	sem := make(chan struct{}, fuzzConcurrency)
	var wg sync.WaitGroup
	for _, path := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// 全局限速（--rate 0 时直通）：fuzz 与正常爬取共享对目标的压力预算
			ratelimit.WaitNavigation(ctx, target)
			foundURL := fuzzProbeOne(base, path)
			if foundURL != "" && onValid != nil {
				onValid(foundURL)
			}
		}(path)
	}
	wg.Wait()
	log.Logger.Infof("[   fuzz   ] 探测完成")
}

// fuzzProbeOne 探测单条路径，有效返回完整 URL，无效返回空。
func fuzzProbeOne(base *url.URL, path string) string {
	probeURL := fmt.Sprintf("%s://%s/%s", base.Scheme, base.Host, path)
	client := fuzzHTTPClient()
	request, err := http.NewRequest(http.MethodGet, probeURL, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("User-Agent", req.WebUserAgent())
	resp, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return probeURL
	}
	if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound {
		location := resp.Header.Get("Location")
		if location == "" {
			return ""
		}
		redirectURL, err := url.Parse(location)
		if err != nil {
			return ""
		}
		// 只认同 host 的重定向（外域跳转多为兜底 302，无意义）
		if redirectURL.Host == base.Host {
			return probeURL
		}
	}
	return ""
}

// fuzzHTTPClient 探测专用 client：短超时、不跟随重定向。
func fuzzHTTPClient() *http.Client {
	transport := &http.Transport{}
	if proxyAddr := req.EffectiveProxy(); proxyAddr != "" {
		if proxyURL, err := url.Parse(proxyAddr); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       fuzzRequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
}
