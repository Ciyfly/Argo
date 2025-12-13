# Argo 爬虫优化指南

> 基于对 Argo 项目的深入分析以及业界优秀爬虫方案（Katana、Crawl4AI、Crawlee）的对比研究

---

## 一、URL 发现能力增强

### 1. JavaScript 解析增强 🔥 高优先级

**当前问题**: `pkg/static/regex.go` 仅使用简单正则匹配 URL，容易遗漏

```go
// 当前实现 - pkg/static/regex.go:8-12
regexStr := `(?i)\b((?:https?://|www\d{0,3}[.]|...)...`
```

**参考 Katana 的解决方案**:
- **集成 [jsluice](https://github.com/BishopFox/jsluice)** 库进行 AST 级别的 JS 解析
- 可以识别：
  - `fetch('/api/users')` 调用
  - `axios.get('/endpoint')`
  - Vue/React Router 定义的路由
  - 字符串拼接的 URL `"/api/" + version + "/users"`

**建议实现**:
```go
// 新增 pkg/static/jsluice.go
import "github.com/BishopFox/jsluice"

func parseJSWithAST(jsContent string) []string {
    analyzer := jsluice.NewAnalyzer([]byte(jsContent))
    var urls []string
    for _, endpoint := range analyzer.GetEndpoints() {
        urls = append(urls, endpoint.URL)
    }
    return urls
}
```

### 2. 添加更多 HTML 属性解析

**当前实现** (`pkg/static/parse.go:16`): 只检查 `href`, `src`, `action`

**遗漏的重要属性**:

| 属性 | 用途 |
|------|------|
| `data-url`, `data-href` | 动态加载 |
| `data-src`, `data-lazy` | 懒加载图片/资源 |
| `srcset` | 响应式图片 |
| `poster` | 视频封面 |
| `hx-get`, `hx-post`, `hx-put`, `hx-patch` | HTMX AJAX |
| `ng-href`, `ng-src` | AngularJS |
| `v-bind:href`, `:href` | Vue.js |
| `formaction` | 表单替代提交地址 |
| `longdesc` | 图片长描述 |
| `cite` | 引用来源 |
| `profile` | 文档元数据 |

**建议修改** `pkg/static/parse.go`:

```go
var urlAttributes = []string{
    "href", "src", "action", "formaction",
    "data-url", "data-href", "data-src", "data-lazy",
    "srcset", "poster", "longdesc", "cite", "profile",
    "hx-get", "hx-post", "hx-put", "hx-patch", "hx-delete",
    "ng-href", "ng-src",
}

func getUrlByTag(t xhtml.Token, currentUrl string) []string {
    attr := t.Attr
    urls := []string{}
    for _, a := range attr {
        if containsIgnoreCase(urlAttributes, a.Key) &&
           !strings.Contains(a.Val, "javascript") &&
           a.Val != "#" {
            if resolved := HandlerUrl(a.Val, currentUrl); resolved != "" {
                urls = append(urls, resolved)
            }
        }
    }
    return urls
}
```

### 3. 增强表单发现与自动提交

**当前问题**: 只提取 `form action`，不解析表单结构

**建议增强** - 新增 `pkg/static/form.go`:

```go
type FormInfo struct {
    Action  string
    Method  string
    EncType string
    Fields  []FormField
}

type FormField struct {
    Name     string
    Type     string
    Value    string
    Required bool
}

func ParseForms(doc *goquery.Document, baseURL string) []FormInfo {
    var forms []FormInfo
    doc.Find("form").Each(func(i int, s *goquery.Selection) {
        form := FormInfo{
            Action:  s.AttrOr("action", ""),
            Method:  strings.ToUpper(s.AttrOr("method", "GET")),
            EncType: s.AttrOr("enctype", "application/x-www-form-urlencoded"),
        }
        // 解析 input, select, textarea
        s.Find("input, select, textarea").Each(func(j int, field *goquery.Selection) {
            form.Fields = append(form.Fields, FormField{
                Name:     field.AttrOr("name", ""),
                Type:     field.AttrOr("type", "text"),
                Value:    field.AttrOr("value", ""),
                Required: field.AttrOr("required", "") != "",
            })
        })
        forms = append(forms, form)
    })
    return forms
}

// 自动填充表单字段
var formFillSuggestions = map[string]string{
    "username": "testuser",
    "email":    "test@example.com",
    "password": "Test123!",
    "phone":    "13800138000",
    "search":   "test",
    "q":        "test",
    "query":    "test",
}
```

### 4. History API 钩子

**问题**: SPA 应用通过 `history.pushState` 修改 URL，不触发页面导航

**建议**: 在 `pkg/inject/auto.go` 的 `AutoJsTemplate` 中添加:

```javascript
// History API 拦截
const originalPushState = history.pushState;
const originalReplaceState = history.replaceState;

history.pushState = function(state, title, url) {
    const result = originalPushState.apply(this, arguments);
    if (url) enqueueUrl(url);
    return result;
};

history.replaceState = function(state, title, url) {
    const result = originalReplaceState.apply(this, arguments);
    if (url) enqueueUrl(url);
    return result;
};

// 监听 popstate 事件
window.addEventListener('popstate', function(event) {
    enqueueUrl(window.location.href);
});

// 监听 hashchange 事件
window.addEventListener('hashchange', function(event) {
    enqueueUrl(window.location.href);
});
```

### 5. XHR/Fetch 请求拦截

**建议**: 在注入脚本中添加网络请求拦截:

```javascript
// Fetch 拦截
const originalFetch = window.fetch;
window.fetch = function(input, init) {
    const url = typeof input === 'string' ? input : input.url;
    if (url && !url.startsWith('data:')) {
        enqueueUrl(url);
    }
    return originalFetch.apply(this, arguments);
};

// XMLHttpRequest 拦截
const originalXHROpen = XMLHttpRequest.prototype.open;
XMLHttpRequest.prototype.open = function(method, url) {
    if (url && !url.startsWith('data:')) {
        enqueueUrl(url);
    }
    return originalXHROpen.apply(this, arguments);
};
```

---

## 二、反检测与稳定性优化

### 1. Stealth 模式增强 🔥 高优先级

**当前问题**: 使用原生 go-rod，容易被反爬检测

**建议集成 stealth 插件**:

```go
// pkg/engine/stealth.go
package engine

import "github.com/go-rod/stealth"

func (ei *EngineInfo) NewStealthPage(browser *rod.Browser, url string) (*rod.Page, error) {
    page, err := stealth.Page(browser)
    if err != nil {
        return nil, err
    }

    // 额外的反检测措施
    page.MustEval(`() => {
        // 隐藏 webdriver 标志
        Object.defineProperty(navigator, 'webdriver', {
            get: () => undefined
        });

        // 模拟真实的插件列表
        Object.defineProperty(navigator, 'plugins', {
            get: () => [1, 2, 3, 4, 5]
        });

        // 模拟真实的语言
        Object.defineProperty(navigator, 'languages', {
            get: () => ['zh-CN', 'zh', 'en-US', 'en']
        });

        // 隐藏自动化特征
        delete window.cdc_adoQpoasnfa76pfcZLmcfl_Array;
        delete window.cdc_adoQpoasnfa76pfcZLmcfl_Promise;
        delete window.cdc_adoQpoasnfa76pfcZLmcfl_Symbol;
    }`)

    return page, nil
}
```

### 2. 代理轮换支持

**当前实现** (`pkg/engine/tab.go:109`): 单一代理

**建议实现代理池**:

```go
// pkg/proxy/pool.go
package proxy

import (
    "sync"
    "sync/atomic"
)

type ProxyPool struct {
    proxies []string
    current uint64
    mu      sync.RWMutex
    failed  map[string]int
}

func NewProxyPool(proxies []string) *ProxyPool {
    return &ProxyPool{
        proxies: proxies,
        failed:  make(map[string]int),
    }
}

// 轮询获取代理
func (p *ProxyPool) Next() string {
    if len(p.proxies) == 0 {
        return ""
    }
    idx := atomic.AddUint64(&p.current, 1)
    return p.proxies[idx%uint64(len(p.proxies))]
}

// 标记代理失败
func (p *ProxyPool) MarkFailed(proxy string) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.failed[proxy]++
    if p.failed[proxy] >= 3 {
        // 移除失败次数过多的代理
        p.removeProxy(proxy)
    }
}

// 获取健康代理
func (p *ProxyPool) GetHealthy() string {
    p.mu.RLock()
    defer p.mu.RUnlock()
    for _, proxy := range p.proxies {
        if p.failed[proxy] < 3 {
            return proxy
        }
    }
    return p.Next() // 回退到轮询
}
```

### 3. 请求频率智能控制

**建议实现自适应限速**:

```go
// pkg/ratelimit/adaptive.go
package ratelimit

import (
    "sync"
    "time"
)

type AdaptiveRateLimiter struct {
    mu              sync.Mutex
    baseInterval    time.Duration
    currentInterval time.Duration
    maxInterval     time.Duration
    minInterval     time.Duration
    successCount    int
    failCount       int
}

func NewAdaptiveRateLimiter(base time.Duration) *AdaptiveRateLimiter {
    return &AdaptiveRateLimiter{
        baseInterval:    base,
        currentInterval: base,
        maxInterval:     base * 10,
        minInterval:     base / 2,
    }
}

func (r *AdaptiveRateLimiter) Wait() {
    r.mu.Lock()
    interval := r.currentInterval
    r.mu.Unlock()
    time.Sleep(interval)
}

func (r *AdaptiveRateLimiter) RecordSuccess() {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.successCount++
    r.failCount = 0
    // 连续成功则加速
    if r.successCount >= 10 {
        r.currentInterval = time.Duration(float64(r.currentInterval) * 0.9)
        if r.currentInterval < r.minInterval {
            r.currentInterval = r.minInterval
        }
        r.successCount = 0
    }
}

func (r *AdaptiveRateLimiter) RecordFailure(statusCode int) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.failCount++
    r.successCount = 0

    // 429 或 503 需要显著降速
    if statusCode == 429 || statusCode == 503 {
        r.currentInterval = r.currentInterval * 2
    } else {
        r.currentInterval = time.Duration(float64(r.currentInterval) * 1.5)
    }

    if r.currentInterval > r.maxInterval {
        r.currentInterval = r.maxInterval
    }
}
```

---

## 三、架构与性能优化

### 1. 双引擎架构（参考 Katana）🔥 高优先级

**当前**: 所有 URL 都用 headless 浏览器

**优化方案**:

```
URL 队列
    ↓
[URL 分类器]
    ├── 静态页面 → Standard Engine (HTTP Client) - 快速、低资源
    └── 动态页面 → Hybrid Engine (Headless Browser) - 完整渲染
```

**实现建议**:

```go
// pkg/engine/classifier.go
package engine

type PageType int

const (
    PageTypeStatic  PageType = iota // 静态页面，用 HTTP 客户端
    PageTypeDynamic                 // 动态页面，用无头浏览器
)

type URLClassifier struct {
    // JS 框架标记
    jsFrameworkPatterns []string
    // 已知的静态扩展名
    staticExtensions map[string]bool
}

func NewURLClassifier() *URLClassifier {
    return &URLClassifier{
        jsFrameworkPatterns: []string{
            "react", "vue", "angular", "ember", "backbone",
            "__NEXT_DATA__", "__NUXT__", "ng-app", "data-reactroot",
        },
        staticExtensions: map[string]bool{
            ".html": true, ".htm": true, ".txt": true,
            ".xml": true, ".json": true, ".rss": true,
        },
    }
}

// ClassifyByURL 根据 URL 预分类
func (c *URLClassifier) ClassifyByURL(url string) PageType {
    // API 端点通常是静态的
    if strings.Contains(url, "/api/") || strings.Contains(url, "/v1/") {
        return PageTypeStatic
    }

    // 检查扩展名
    ext := path.Ext(url)
    if c.staticExtensions[ext] {
        return PageTypeStatic
    }

    // 默认动态处理
    return PageTypeDynamic
}

// ClassifyByResponse 根据响应内容分类
func (c *URLClassifier) ClassifyByResponse(body []byte, contentType string) PageType {
    // JSON/XML API 响应
    if strings.Contains(contentType, "application/json") ||
       strings.Contains(contentType, "application/xml") {
        return PageTypeStatic
    }

    bodyStr := string(body)
    // 检查 JS 框架标记
    for _, pattern := range c.jsFrameworkPatterns {
        if strings.Contains(bodyStr, pattern) {
            return PageTypeDynamic
        }
    }

    // 检查大量 script 标签
    scriptCount := strings.Count(bodyStr, "<script")
    if scriptCount > 5 {
        return PageTypeDynamic
    }

    return PageTypeStatic
}
```

**Standard Engine 实现**:

```go
// pkg/engine/standard.go
package engine

import (
    "io"
    "net/http"
)

type StandardEngine struct {
    client    *http.Client
    parser    *URLParser
    rateLimit *AdaptiveRateLimiter
}

func (e *StandardEngine) Crawl(url string) ([]string, error) {
    e.rateLimit.Wait()

    req, err := http.NewRequest("GET", url, nil)
    if err != nil {
        return nil, err
    }

    req.Header.Set("User-Agent", randomUserAgent())

    resp, err := e.client.Do(req)
    if err != nil {
        e.rateLimit.RecordFailure(0)
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 400 {
        e.rateLimit.RecordFailure(resp.StatusCode)
        return nil, fmt.Errorf("status code: %d", resp.StatusCode)
    }

    e.rateLimit.RecordSuccess()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }

    // 解析 URL
    return e.parser.Parse(body, url), nil
}
```

### 2. 浏览器实例复用

**当前问题** (`pkg/engine/tab.go:66-74`): 每个 URL 创建新浏览器实例

**建议实现浏览器池**:

```go
// pkg/engine/browser_pool.go
package engine

import (
    "sync"
    "github.com/go-rod/rod"
)

type BrowserPool struct {
    mu       sync.Mutex
    browsers []*rod.Browser
    maxSize  int
    launcher *launcher.Launcher
}

func NewBrowserPool(size int) *BrowserPool {
    pool := &BrowserPool{
        maxSize:  size,
        browsers: make([]*rod.Browser, 0, size),
    }
    // 预热浏览器实例
    for i := 0; i < size; i++ {
        browser := pool.createBrowser()
        if browser != nil {
            pool.browsers = append(pool.browsers, browser)
        }
    }
    return pool
}

func (p *BrowserPool) createBrowser() *rod.Browser {
    l := launcher.New().
        Headless(true).
        Set("disable-gpu").
        Set("no-sandbox").
        Set("disable-dev-shm-usage")

    browser := rod.New().ControlURL(l.MustLaunch()).MustConnect()
    browser.MustIgnoreCertErrors(true)
    return browser
}

func (p *BrowserPool) Get() *rod.Browser {
    p.mu.Lock()
    defer p.mu.Unlock()

    if len(p.browsers) > 0 {
        browser := p.browsers[len(p.browsers)-1]
        p.browsers = p.browsers[:len(p.browsers)-1]
        return browser
    }

    // 池空了，创建新的
    return p.createBrowser()
}

func (p *BrowserPool) Put(browser *rod.Browser) {
    p.mu.Lock()
    defer p.mu.Unlock()

    if len(p.browsers) < p.maxSize {
        // 清理状态后放回池
        p.cleanBrowser(browser)
        p.browsers = append(p.browsers, browser)
    } else {
        // 池满了，关闭
        browser.Close()
    }
}

func (p *BrowserPool) cleanBrowser(browser *rod.Browser) {
    // 清理 cookies
    browser.MustSetCookies()
    // 清理本地存储等
}

func (p *BrowserPool) Close() {
    p.mu.Lock()
    defer p.mu.Unlock()

    for _, browser := range p.browsers {
        browser.Close()
    }
    p.browsers = nil
}
```

### 3. 增量爬取支持

**建议实现**:

```go
// pkg/engine/incremental.go
package engine

import (
    "encoding/json"
    "os"
    "time"
)

type CrawlState struct {
    LastCrawl    time.Time         `json:"last_crawl"`
    CrawledURLs  map[string]int64  `json:"crawled_urls"` // URL -> 时间戳
    PendingURLs  []string          `json:"pending_urls"`
    StateFile    string            `json:"-"`
}

func LoadCrawlState(file string) (*CrawlState, error) {
    state := &CrawlState{
        CrawledURLs: make(map[string]int64),
        StateFile:   file,
    }

    data, err := os.ReadFile(file)
    if err != nil {
        if os.IsNotExist(err) {
            return state, nil
        }
        return nil, err
    }

    if err := json.Unmarshal(data, state); err != nil {
        return nil, err
    }

    return state, nil
}

func (s *CrawlState) Save() error {
    s.LastCrawl = time.Now()
    data, err := json.MarshalIndent(s, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(s.StateFile, data, 0644)
}

func (s *CrawlState) ShouldCrawl(url string, maxAge time.Duration) bool {
    timestamp, exists := s.CrawledURLs[url]
    if !exists {
        return true
    }

    lastCrawl := time.Unix(timestamp, 0)
    return time.Since(lastCrawl) > maxAge
}

func (s *CrawlState) MarkCrawled(url string) {
    s.CrawledURLs[url] = time.Now().Unix()
}
```

---

## 四、URL 去重与规范化优化

### 1. 更智能的 URL 泛化

**当前实现** (`pkg/engine/normalize.go:171-198`): 基础模式替换

**建议增强**:

```go
// pkg/engine/normalize.go 增强版

var additionalPatterns = []struct {
    Regex   *regexp.Regexp
    Replace string
}{
    // 时间戳 (10-13位数字)
    {regexp.MustCompile(`\d{10,13}`), "{timestamp}"},
    // 版本号
    {regexp.MustCompile(`v\d+(\.\d+)*`), "{version}"},
    // Base64 编码
    {regexp.MustCompile(`[A-Za-z0-9+/]{20,}={0,2}`), "{base64}"},
    // JWT Token
    {regexp.MustCompile(`eyJ[A-Za-z0-9_-]*\.eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*`), "{jwt}"},
    // MongoDB ObjectId
    {regexp.MustCompile(`[0-9a-fA-F]{24}`), "{objectid}"},
    // 短 hash (git commit 等)
    {regexp.MustCompile(`[0-9a-fA-F]{7,8}`), "{shorthash}"},
    // 分页参数
    {regexp.MustCompile(`page[=_]?\d+`), "page={num}"},
    {regexp.MustCompile(`offset[=_]?\d+`), "offset={num}"},
    {regexp.MustCompile(`limit[=_]?\d+`), "limit={num}"},
    // 排序参数
    {regexp.MustCompile(`sort=(asc|desc|ASC|DESC)`), "sort={order}"},
}

func classifyTokenEnhanced(token string) string {
    if token == "" {
        return token
    }

    clean := strings.TrimSpace(token)

    // 基础类型检测
    if isNumber(clean) {
        return "{num}"
    }
    if uuidRegex.MatchString(clean) {
        return "{uuid}"
    }
    if dateRegex.MatchString(clean) {
        return "{date}"
    }
    if hexRegex.MatchString(clean) && len(clean) >= 8 {
        return "{hex}"
    }

    // 增强模式检测
    for _, pattern := range additionalPatterns {
        if pattern.Regex.MatchString(clean) {
            return pattern.Replace
        }
    }

    if !looksLikeSlug(clean) {
        if len(clean) > 40 {
            return "{token}"
        }
        return strings.ToLower(clean)
    }
    return strings.ToLower(clean)
}
```

### 2. URL 相似度去重

**建议使用 SimHash 算法**:

```go
// pkg/engine/simhash.go
package engine

import (
    "hash/fnv"
    "strings"
)

type SimHash struct {
    threshold float64
    hashes    map[uint64]bool
}

func NewSimHash(threshold float64) *SimHash {
    return &SimHash{
        threshold: threshold,
        hashes:    make(map[uint64]bool),
    }
}

func (s *SimHash) Hash(url string) uint64 {
    // 分词
    tokens := tokenize(url)

    // 计算 SimHash
    var v [64]int
    for _, token := range tokens {
        h := hash64(token)
        for i := 0; i < 64; i++ {
            if (h>>i)&1 == 1 {
                v[i]++
            } else {
                v[i]--
            }
        }
    }

    var simhash uint64
    for i := 0; i < 64; i++ {
        if v[i] > 0 {
            simhash |= 1 << i
        }
    }

    return simhash
}

func (s *SimHash) IsSimilar(url string) bool {
    h := s.Hash(url)

    for existing := range s.hashes {
        if hammingDistance(h, existing) <= 3 { // 汉明距离阈值
            return true
        }
    }

    s.hashes[h] = true
    return false
}

func tokenize(url string) []string {
    // URL 分词：按 /, ?, &, = 分割
    separators := []string{"/", "?", "&", "=", "-", "_"}
    tokens := []string{url}

    for _, sep := range separators {
        var newTokens []string
        for _, token := range tokens {
            newTokens = append(newTokens, strings.Split(token, sep)...)
        }
        tokens = newTokens
    }

    // 过滤空串和数字
    var result []string
    for _, t := range tokens {
        if t != "" && !isNumber(t) {
            result = append(result, strings.ToLower(t))
        }
    }

    return result
}

func hash64(s string) uint64 {
    h := fnv.New64a()
    h.Write([]byte(s))
    return h.Sum64()
}

func hammingDistance(a, b uint64) int {
    xor := a ^ b
    count := 0
    for xor != 0 {
        count++
        xor &= xor - 1
    }
    return count
}
```

---

## 五、新功能建议

### 1. WebSocket 支持

```go
// pkg/engine/websocket.go
package engine

import (
    "github.com/go-rod/rod"
    "github.com/go-rod/rod/lib/proto"
)

func (ei *EngineInfo) MonitorWebSocket(page *rod.Page) {
    go page.EachEvent(func(e *proto.NetworkWebSocketCreated) {
        log.Logger.Debugf("WebSocket created: %s", e.URL)
        // 记录 WebSocket URL
        ei.PushStaticUrl(&UrlInfo{
            Url:        e.URL,
            SourceType: "websocket",
        })
    })()

    go page.EachEvent(func(e *proto.NetworkWebSocketFrameReceived) {
        // 解析 WebSocket 消息中的 URL
        urls := extractURLsFromText(e.Response.PayloadData)
        for _, url := range urls {
            ei.PushStaticUrl(&UrlInfo{
                Url:        url,
                SourceType: "websocket_message",
            })
        }
    })()
}
```

### 2. Service Worker/PWA 支持

```go
// pkg/static/pwa.go
package static

import (
    "encoding/json"
)

type WebAppManifest struct {
    StartURL     string `json:"start_url"`
    Scope        string `json:"scope"`
    Icons        []Icon `json:"icons"`
    ServiceWorker struct {
        Src   string `json:"src"`
        Scope string `json:"scope"`
    } `json:"serviceworker"`
}

type Icon struct {
    Src   string `json:"src"`
    Sizes string `json:"sizes"`
    Type  string `json:"type"`
}

func ParseManifest(data []byte, baseURL string) []string {
    var manifest WebAppManifest
    if err := json.Unmarshal(data, &manifest); err != nil {
        return nil
    }

    var urls []string

    if manifest.StartURL != "" {
        if url, err := resolveURL(manifest.StartURL, baseURL); err == nil {
            urls = append(urls, url)
        }
    }

    if manifest.ServiceWorker.Src != "" {
        if url, err := resolveURL(manifest.ServiceWorker.Src, baseURL); err == nil {
            urls = append(urls, url)
        }
    }

    for _, icon := range manifest.Icons {
        if url, err := resolveURL(icon.Src, baseURL); err == nil {
            urls = append(urls, url)
        }
    }

    return urls
}
```

### 3. GraphQL 端点发现

```go
// pkg/static/graphql.go
package static

import (
    "bytes"
    "encoding/json"
    "net/http"
)

var graphqlEndpoints = []string{
    "/graphql",
    "/gql",
    "/api/graphql",
    "/v1/graphql",
}

var introspectionQuery = `{
    __schema {
        types {
            name
            fields {
                name
            }
        }
        queryType { name }
        mutationType { name }
    }
}`

func DiscoverGraphQL(baseURL string, client *http.Client) *GraphQLSchema {
    for _, endpoint := range graphqlEndpoints {
        url := baseURL + endpoint
        schema := tryIntrospection(url, client)
        if schema != nil {
            return schema
        }
    }
    return nil
}

func tryIntrospection(url string, client *http.Client) *GraphQLSchema {
    query := map[string]string{"query": introspectionQuery}
    body, _ := json.Marshal(query)

    req, err := http.NewRequest("POST", url, bytes.NewReader(body))
    if err != nil {
        return nil
    }
    req.Header.Set("Content-Type", "application/json")

    resp, err := client.Do(req)
    if err != nil || resp.StatusCode != 200 {
        return nil
    }
    defer resp.Body.Close()

    var result struct {
        Data struct {
            Schema GraphQLSchema `json:"__schema"`
        } `json:"data"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil
    }

    return &result.Data.Schema
}
```

### 4. API 端点参数发现

```go
// pkg/static/api_discovery.go
package static

import (
    "regexp"
    "strings"
)

var apiPatterns = []*regexp.Regexp{
    regexp.MustCompile(`(?i)/api/v?\d*/?\w+`),
    regexp.MustCompile(`(?i)/rest/\w+`),
    regexp.MustCompile(`(?i)\.(json|xml)$`),
}

type APIEndpoint struct {
    URL        string
    Method     string
    Parameters []string
}

func ExtractAPIEndpoints(content string, baseURL string) []APIEndpoint {
    var endpoints []APIEndpoint

    for _, pattern := range apiPatterns {
        matches := pattern.FindAllString(content, -1)
        for _, match := range matches {
            endpoint := APIEndpoint{
                URL:    resolveToBase(match, baseURL),
                Method: guessMethod(match),
            }
            endpoints = append(endpoints, endpoint)
        }
    }

    // 从 OpenAPI/Swagger 文件提取
    if strings.Contains(content, "swagger") || strings.Contains(content, "openapi") {
        endpoints = append(endpoints, parseSwagger(content, baseURL)...)
    }

    return endpoints
}

func guessMethod(path string) string {
    lower := strings.ToLower(path)
    if strings.Contains(lower, "create") || strings.Contains(lower, "add") {
        return "POST"
    }
    if strings.Contains(lower, "update") || strings.Contains(lower, "edit") {
        return "PUT"
    }
    if strings.Contains(lower, "delete") || strings.Contains(lower, "remove") {
        return "DELETE"
    }
    return "GET"
}
```

---

## 六、BUG 修复建议

### 1. 资源泄露风险

**位置**: `pkg/engine/tab.go:79`

```go
router := browser.HijackRequests()
defer router.Stop()  // 问题：在 goroutine 之后可能提前执行
```

**修复建议**:

```go
func (ei *EngineInfo) NewTab(uif *UrlInfo, pageFlag int) {
    // ... 初始化代码 ...

    router := browser.HijackRequests()
    // 不使用 defer，改为显式关闭

    // 在 goroutine 完成后关闭
    go func() {
        defer router.Stop()  // 移到 goroutine 内部
        router.Run()
    }()

    // 或者使用 context 控制
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go router.Run()

    // ... 其他逻辑 ...
}
```

### 2. 并发安全

**位置**: `pkg/engine/normalize.go:74-79`

```go
// 问题：非原子操作，可能导致数据竞争
if len(ei.NormalizeationResultMap) > normalizeCacheLimit {
    ei.NormalizeationResultMap = make(map[string]int)
}
```

**修复建议**:

```go
// 方案1：使用 sync.Map
type EngineInfo struct {
    // ...
    NormalizeationResultMap sync.Map
    // ...
}

func (ei *EngineInfo) normalizeWork() {
    for data := range ei.PendingNormalizeQueue {
        // ...
        value := normalizeation(urlStr, data.Method)
        if _, loaded := ei.NormalizeationResultMap.LoadOrStore(value, 0); !loaded {
            ei.pushResult(data)
        }
    }
}

// 方案2：添加互斥锁（当前方案的改进）
type EngineInfo struct {
    // ...
    normalizeMapMu          sync.RWMutex
    NormalizeationResultMap map[string]int
    // ...
}

func (ei *EngineInfo) normalizeWork() {
    for data := range ei.PendingNormalizeQueue {
        // ...
        value := normalizeation(urlStr, data.Method)

        ei.normalizeMapMu.Lock()
        if _, ok := ei.NormalizeationResultMap[value]; !ok {
            ei.NormalizeationResultMap[value] = 0
            if len(ei.NormalizeationResultMap) > normalizeCacheLimit {
                ei.NormalizeationResultMap = make(map[string]int)
            }
            ei.normalizeMapMu.Unlock()
            ei.pushResult(data)
        } else {
            ei.normalizeMapMu.Unlock()
        }
    }
}
```

### 3. 错误处理改进

**位置**: `pkg/static/parse.go:83-96`

```go
// 问题：静默失败
func ParseDom(page *rod.Page) []string {
    target, err := utils.GetCurrentUrlByPage(page)
    if err != nil {
        return nil  // 应该记录日志
    }
```

**修复建议**:

```go
func ParseDom(page *rod.Page) []string {
    target, err := utils.GetCurrentUrlByPage(page)
    if err != nil {
        log.Logger.Warnf("ParseDom: failed to get current URL: %v", err)
        return nil
    }

    log.Logger.Debugf("parse dom %s", target)

    htmlStr, err := page.HTML()
    if err != nil {
        log.Logger.Errorf("ParseDom: failed to get HTML for %s: %v", target, err)
        return nil
    }

    if htmlStr == "" {
        log.Logger.Warnf("ParseDom: empty HTML for %s", target)
        return nil
    }

    return ParseHtml(htmlStr, target)
}
```

### 4. 内存泄漏风险

**位置**: `pkg/engine/normalize.go:29` - `normalizeCacheLimit = 500000`

**问题**: 缓存达到限制后直接清空，可能导致重复爬取

**修复建议**: 使用 LRU 缓存

```go
// 使用 golang-lru
import lru "github.com/hashicorp/golang-lru"

type EngineInfo struct {
    // ...
    NormalizeationCache *lru.Cache
    // ...
}

func (ei *EngineInfo) InitNormalize() {
    cache, _ := lru.New(normalizeCacheLimit)
    ei.NormalizeationCache = cache
    // ...
}

func (ei *EngineInfo) normalizeWork() {
    for data := range ei.PendingNormalizeQueue {
        // ...
        value := normalizeation(urlStr, data.Method)
        if _, ok := ei.NormalizeationCache.Get(value); !ok {
            ei.NormalizeationCache.Add(value, struct{}{})
            ei.pushResult(data)
        }
    }
}
```

---

## 七、配置增强建议

在 `pkg/conf/conf.go` 中添加新配置项：

```yaml
# configs/config.yml 新增配置

# 双引擎配置
engine:
  mode: hybrid           # standard | hybrid | browser-only
  static_extensions:     # 使用标准引擎的扩展名
    - .html
    - .json
    - .xml

# 代理池配置
proxy:
  enabled: false
  rotation: round-robin  # round-robin | random | smart
  pool:
    - http://proxy1:8080
    - http://proxy2:8080
  health_check_interval: 60s

# 反检测配置
stealth:
  enabled: true
  random_user_agent: true
  webdriver_hide: true

# 增量爬取配置
incremental:
  enabled: false
  state_file: .argo_state.json
  max_age: 24h

# 自适应限速
rate_limit:
  adaptive: true
  base_interval: 500ms
  min_interval: 100ms
  max_interval: 5s

# 浏览器池
browser_pool:
  enabled: true
  size: 5
  pre_warm: true
```

---

## 八、优先级排序

| 优先级 | 优化项 | 预期收益 | 实现难度 | 状态 |
|--------|--------|----------|----------|------|
| **P0** | JSluice 集成 | +30% URL 发现 | 中 | ✅ 已完成 |
| **P0** | HTML 属性扩展 | +15% URL 发现 | 低 | ✅ 已完成 |
| **P0** | 并发安全修复 | 稳定性 | 低 | ✅ 已完成 |
| **P0** | 浏览器池 | +30% 性能 | 中 | ✅ 已完成 |
| **P0** | 自适应并发控制 | 智能化 | 中 | ✅ 已完成 |
| **P0** | 响应缓存 | +20% 性能 | 低 | ✅ 已完成 |
| **P0** | 资源过滤器 | 减少资源消耗 | 低 | ✅ 已完成 |
| **P1** | 双引擎架构 | +50% 性能 | 高 | ✅ 已完成 |
| **P1** | Stealth 模式 | 降低被封率 | 低 | ✅ 已完成 |
| **P1** | 增强反检测 | 降低被封率 | 中 | ✅ 已完成 |
| **P1** | 智能表单填充 | +20% URL 发现 | 中 | ✅ 已完成 |
| **P1** | 被动爬取 | +15% URL 发现 | 中 | ✅ 已完成 |
| **P2** | JS 静态分析器 | +25% URL 发现 | 中 | ✅ 已完成 |
| **P2** | 智能深度控制 | 效率提升 | 中 | ✅ 已完成 |
| **P2** | 自适应限速 | 稳定性提升 | 中 | ✅ 已完成 |
| **P2** | 敏感信息检测 | 安全增强 | 中 | ✅ 已完成 |
| **P3** | WebSocket 支持 | 完整性 | 中 | ✅ 已完成 |
| **P3** | 增量爬取 | 效率提升 | 中 | ✅ 已完成 |
| **P3** | GraphQL 支持 | API 覆盖 | 中 | ✅ 已完成 |
| **P3** | Swagger/OpenAPI 支持 | API 覆盖 | 中 | ✅ 已完成 |
| **P4** | 被动源发现 (Wayback/CommonCrawl) | +40% URL 发现 | 中 | ✅ 已完成 |
| **P4** | 代理池轮换 | 稳定性提升 | 低 | ✅ 已完成 |
| **P4** | 作用域控制 | 精准爬取 | 低 | ✅ 已完成 |
| **P4** | 路径爬升 (Path Climbing) | +15% URL 发现 | 低 | ✅ 已完成 |
| **P4** | 框架指纹识别 | 智能引擎切换 | 低 | ✅ 已完成 |

---

## 九、已完成优化详情

### P0 优化 (核心性能)

#### 1. JSluice/AST 级别 JS 解析
- **文件**: `pkg/static/jsluice.go`
- **功能**: 使用 JSluice 库进行 AST 级别的 JavaScript 分析
- **提取内容**: API 端点、fetch/axios 调用、路由定义、敏感信息

#### 2. 浏览器池 (Browser Pool)
- **文件**: `pkg/engine/browser_pool.go`
- **功能**: 复用浏览器实例，减少启动开销
- **配置**: `browser.enable_pool`, `browser.pool_max_instances`

#### 3. 自适应并发控制 (Autoscaler)
- **文件**: `pkg/engine/autoscaler.go`
- **功能**: 根据系统负载动态调整并发数
- **指标**: CPU、内存、错误率

#### 4. 响应缓存 (Response Cache)
- **文件**: `pkg/engine/cache.go`
- **功能**: 缓存静态资源响应，避免重复请求

#### 5. 资源过滤器 (Resource Filter)
- **文件**: `pkg/engine/cache.go`
- **功能**: 过滤图片、字体、视频等大型静态资源

### P1 优化 (功能增强)

#### 1. 双引擎架构 (Dual Engine)
- **文件**: `pkg/engine/dual_engine.go`
- **功能**:
  - Standard Engine (HTTP Client): 处理 API、静态资源
  - Hybrid Engine (Browser): 处理 SPA、动态页面
- **URL 分类器**: 自动判断最佳引擎
- **配置**: `dual_engine.enabled`, `dual_engine.standard_workers`

#### 2. 增强反检测 (Enhanced Stealth)
- **文件**: `pkg/engine/stealth_enhanced.go`
- **功能**:
  - WebDriver 检测规避
  - Navigator 属性伪装
  - 随机 User-Agent
  - Canvas 指纹随机化

#### 3. 智能表单填充 (Smart Form Filler)
- **文件**: `pkg/engine/smart_form.go`
- **功能**: 根据字段名智能填充表单数据

#### 4. 被动爬取 (Passive Crawler)
- **文件**: `pkg/engine/passive_crawler.go`
- **功能**: 监听网络请求，被动收集 URL

### P2 优化 (智能化)

#### 1. JS 静态分析器
- **文件**: `pkg/engine/js_analyzer.go`
- **功能**:
  - URL 模式提取
  - 敏感信息检测 (120+ 正则模式)
  - API Key、Token、密码等

#### 2. 智能深度控制
- **文件**: `pkg/engine/smart_depth.go`
- **功能**: 根据页面价值动态调整爬取深度

#### 3. 自适应限速
- **文件**: `pkg/ratelimit/adaptive.go`, `pkg/engine/dual_engine.go`
- **功能**:
  - 每域名独立限速器
  - 根据响应状态自动调整
  - 429/503 错误自动降速
  - 连续成功自动加速
- **配置**: `rate_limit.enabled`, `rate_limit.base_interval_ms`

### P3 优化 (完整性)

#### 1. WebSocket 支持
- **文件**: `pkg/engine/websocket.go`
- **功能**:
  - 监控 WebSocket 连接
  - 捕获发送/接收消息
  - 提取 WebSocket 端点
  - 统计连接和消息数量
- **配置**: `websocket.enabled`, `websocket.capture_messages`

#### 2. 增量爬取 (Incremental Crawling)
- **文件**: `pkg/engine/incremental.go`
- **功能**:
  - 爬取状态持久化 (JSON/GZIP)
  - 断点续爬支持
  - URL过期重爬机制
  - 自动保存检查点
  - 待爬取队列恢复
  - 失败URL重试管理
- **配置**: `incremental.enabled`, `incremental.max_age_hours`, `incremental.resume_from_pending`
- **状态文件内容**:
  - 已爬取URL及元信息
  - 待爬取URL队列
  - 失败URL记录
  - 爬取统计信息

#### 3. GraphQL 端点发现 (GraphQL Discovery)
- **文件**: `pkg/engine/graphql.go`
- **功能**:
  - 自动发现 GraphQL 端点 (常见路径扫描)
  - 内省查询获取完整 Schema
  - 从 JavaScript 提取 GraphQL 查询
  - 生成示例查询
  - Schema 解析 (类型、字段、参数、枚举)
- **配置**: `graphql.enabled`, `graphql.auto_discover`, `graphql.enable_introspection`
- **发现路径**: `/graphql`, `/gql`, `/api/graphql`, `/v1/graphql` 等
- **提取内容**:
  - 端点 URL 和验证状态
  - 内省 Schema (Query/Mutation/Subscription 类型)
  - JS 中的 gql 模板查询
  - JSON 中的 query 字段

#### 4. Swagger/OpenAPI 支持 (Swagger Discovery)
- **文件**: `pkg/engine/swagger.go`
- **功能**:
  - 自动发现 Swagger/OpenAPI 规范文件 (40+ 常见路径扫描)
  - 支持 Swagger 2.0 和 OpenAPI 3.x 规范
  - 从 Swagger UI 页面提取规范 URL
  - 完整规范解析 (paths, operations, parameters, responses)
  - 智能示例请求生成
  - API 端点自动提交给爬虫
- **配置**: `swagger.enabled`, `swagger.auto_discover`, `swagger.parse_spec`, `swagger.generate_requests`
- **发现路径**:
  - Swagger 2.0: `/swagger.json`, `/api-docs`, `/v2/api-docs` 等
  - OpenAPI 3.x: `/openapi.json`, `/v3/api-docs` 等
  - Swagger UI: `/swagger-ui.html`, `/docs`, `/documentation` 等
- **提取内容**:
  - API 规范元信息 (标题、版本、描述)
  - 所有 API 端点 (URL、Method、参数、Content-Type)
  - 示例请求 (根据参数类型智能生成)
  - 安全定义 (认证方式)

### P4 优化 (竞品对齐)

#### 1. 被动源发现 (Passive Source Discovery)
- **文件**: `pkg/engine/passive_sources.go`
- **功能**:
  - Wayback Machine 历史 URL 查询
  - Common Crawl 索引查询
  - AlienVault OTX 威胁情报
  - VirusTotal 域名报告 (需 API Key)
  - URLScan.io 搜索
  - URL 去重和规范化
  - 跟踪参数过滤
- **配置**: `passive_sources.enabled`, `passive_sources.wayback`, `passive_sources.virustotal_api_key`
- **提取内容**:
  - 历史 URL 及来源
  - JS 文件和 API 端点
  - 按正则或扩展名过滤

#### 2. 代理池轮换 (Proxy Pool)
- **文件**: `pkg/proxy/pool.go`
- **功能**:
  - 从文件/字符串加载代理列表
  - Round-Robin/Random/Smart 轮换策略
  - 代理健康检查
  - 失败代理自动剔除
  - 成功率和延迟统计
  - 智能选择最优代理
- **配置**: `proxy_pool.enabled`, `proxy_pool.file`, `proxy_pool.rotation`
- **轮换策略**:
  - `round-robin`: 顺序轮换
  - `random`: 随机选择健康代理
  - `smart`: 基于成功率和延迟评分选择

#### 3. 作用域控制 (Scope Controller)
- **文件**: `pkg/engine/scope.go`
- **功能**:
  - 域名白名单/黑名单
  - 子域名自动包含
  - 路径前缀过滤
  - 正则模式匹配
  - 扩展名过滤 (图片/视频/文档)
  - CDN 域名自动排除 (30+ 常见 CDN)
  - 私有 IP 排除
  - 爬取深度限制
- **配置**: `scope.include_domains`, `scope.exclude_cdn`, `scope.max_depth`
- **默认排除**:
  - 图片: `.png`, `.jpg`, `.gif`, `.svg`, `.webp` 等
  - 字体: `.woff`, `.woff2`, `.ttf`, `.eot` 等
  - 媒体: `.mp4`, `.mp3`, `.avi`, `.mov` 等
  - 文档: `.pdf`, `.doc`, `.xlsx`, `.ppt` 等
  - 压缩包: `.zip`, `.rar`, `.7z`, `.tar`, `.gz` 等

#### 4. 路径爬升 (Path Climbing)
- **文件**: `pkg/engine/path_climber.go`
- **功能**:
  - 从深层路径向上生成父路径
  - 例: `/api/v1/users/123/profile` → `/api/v1/users/123`, `/api/v1/users`, `/api/v1`, `/api`
  - API 版本变体生成 (`/v1` → `/v2`, `/v3`)
  - 常见端点后缀尝试 (`/api`, `/graphql`, `/swagger.json`)
  - 参数值变体生成 (数字参数尝试 0, 1, -1, 999999)
  - URL 去重防止重复爬取
- **配置**: `path_climbing.enabled`, `path_climbing.max_climb_depth`
- **用途**:
  - 发现未直接链接的目录
  - 探索 API 版本
  - 测试参数边界

#### 5. 框架指纹识别 (Framework Detection)
- **文件**: `pkg/engine/framework_detector.go`
- **功能**:
  - 自动识别前端框架 (React, Vue, Angular, Next.js, Nuxt.js, Svelte, Ember)
  - 识别后端框架 (WordPress, Drupal, Laravel, Django, Rails, Spring)
  - 识别库 (jQuery, Backbone)
  - SPA 检测和浏览器引擎自动切换
  - 检测置信度评分
  - Header/Cookie/Path 多维度检测
- **配置**: `framework_detection.enabled`, `framework_detection.auto_switch_engine`
- **检测方式**:
  - HTML 特征 (`data-reactroot`, `v-bind:`, `ng-app`)
  - HTTP Header (`X-Powered-By: Next.js`)
  - Cookie (`laravel_session`, `JSESSIONID`)
  - URL 路径 (`/wp-content/`, `/actuator/`)
- **智能决策**:
  - 检测到 SPA 框架 → 使用浏览器引擎
  - 检测到传统框架 → 使用 HTTP 客户端

---

## 十、配置参考

```yaml
# config.yml 完整配置示例

browser:
  tabcount: 10
  tabtimeout: 240
  browsertimeout: 18000
  maxdepth: 10
  enable_pool: true
  pool_max_instances: 5
  pool_max_tabs: 10

dual_engine:
  enabled: true
  standard_workers: 20

rate_limit:
  enabled: true
  base_interval_ms: 200
  min_interval_ms: 50
  max_interval_ms: 10000

websocket:
  enabled: true
  max_connections: 100
  max_messages_per_conn: 50
  capture_messages: true

incremental:
  enabled: false
  state_file: ""
  max_age_hours: 24
  auto_save_interval_min: 5
  resume_from_pending: true

graphql:
  enabled: true
  auto_discover: true
  enable_introspection: true
  max_depth: 3
  timeout_ms: 10000

swagger:
  enabled: true
  auto_discover: true
  parse_spec: true
  generate_requests: true
  timeout_ms: 15000

# P4优化: 被动源发现
passive_sources:
  enabled: false           # 启用历史URL发现
  wayback: true            # Wayback Machine
  common_crawl: true       # Common Crawl
  alienvault: true         # AlienVault OTX
  virustotal: false        # VirusTotal (需要API Key)
  urlscan: false           # URLScan.io
  timeout_ms: 30000
  max_results: 10000
  include_subdomains: true
  virustotal_api_key: ""   # VirusTotal API Key

# P4优化: 作用域控制
scope:
  include_subdomains: true  # 包含子域名
  exclude_cdn: true         # 排除CDN域名
  exclude_external: false   # 排除外部链接
  max_depth: 10             # 最大爬取深度
  # include_domains: []     # 域名白名单
  # exclude_domains: []     # 域名黑名单
  # include_paths: []       # 路径前缀白名单
  # exclude_paths: []       # 路径前缀黑名单

# P4优化: 路径爬升
path_climbing:
  enabled: true             # 启用路径爬升
  max_climb_depth: 5        # 最大爬升层数

# P4优化: 框架检测
framework_detection:
  enabled: true             # 启用框架检测
  auto_switch_engine: true  # 自动切换引擎

# P4优化: 代理池
proxy_pool:
  enabled: false            # 启用代理池
  file: ""                  # 代理列表文件
  proxies: ""               # 代理列表(逗号分隔)
  rotation: "round-robin"   # 轮换策略: round-robin, random, smart
  max_fails: 3              # 最大失败次数
  health_check: false       # 启用健康检查
  health_check_interval_sec: 60
```

---

## 十一、参考资源

- [Katana - ProjectDiscovery](https://github.com/projectdiscovery/katana) - Go 语言下一代爬虫框架
- [Crawl4AI](https://github.com/unclecode/crawl4ai) - LLM 友好的爬虫
- [Crawlee](https://github.com/apify/crawlee) - Node.js 高性能爬虫库
- [jsluice](https://github.com/BishopFox/jsluice) - JS 静态分析提取 URL
- [go-rod/stealth](https://github.com/nicobao/rod-stealth) - Rod 反检测插件
- [Puppeteer Optimization Guide](https://scrapeops.io/puppeteer-web-scraping-playbook/nodejs-puppeteer-optimize-puppeteer/)
- [Web Scraping Best Practices](https://www.scrapingbee.com/blog/what-is-a-headless-browser-best-solutions-for-web-scraping-at-scale/)

---

## 十二、总结

Argo 爬虫经过全面优化后，已具备以下能力：

### 已实现的核心优化

1. **URL 发现增强**:
   - JSluice AST 级别 JS 解析 (+30% URL 发现)
   - 120+ 敏感信息正则检测模式
   - 智能表单自动填充
   - 被动网络监听

2. **性能优化**:
   - 双引擎架构 (HTTP Client + Browser)
   - 浏览器实例池 (减少启动开销)
   - 响应缓存和资源过滤
   - 自适应并发控制

3. **稳定性提升**:
   - 增强反检测 (WebDriver 规避、指纹随机化)
   - 自适应限速 (429/503 自动降速)
   - 每域名独立限速器

4. **功能完整性**:
   - WebSocket 连接监控和消息捕获
   - 增量爬取和断点续爬
   - 智能深度控制
   - GraphQL 端点发现和内省
   - Swagger/OpenAPI 规范发现和解析
   - 全面的 Metrics 统计输出

### 架构改进

```
                    ┌─────────────────┐
                    │   URL 队列      │
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  URL 分类器     │
                    └────────┬────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
    ┌─────────▼─────────┐       ┌──────────▼──────────┐
    │  Standard Engine  │       │   Hybrid Engine     │
    │   (HTTP Client)   │       │    (Browser)        │
    │                   │       │                     │
    │ • API 端点        │       │ • SPA 页面          │
    │ • JSON/XML        │       │ • 动态渲染          │
    │ • 静态资源        │       │ • WebSocket         │
    │ • GraphQL 发现    │       │ • 自动交互          │
    │ • 自适应限速      │       │ • GraphQL 内省      │
    └─────────┬─────────┘       └──────────┬──────────┘
              │                             │
              └──────────────┬──────────────┘
                             │
                    ┌────────▼────────┐
                    │   结果聚合      │
                    └─────────────────┘
```

### 待实现优化

(所有主要优化已完成)

---

## 十三、竞品对比总结

经过 P0-P4 优化后，Argo 与业界主流爬虫的功能对比：

| 功能 | Argo | Katana | Gospider | Hakrawler | Burp Pro | Acunetix |
|------|------|--------|----------|-----------|----------|----------|
| 浏览器引擎 | ✅ | ✅ | ❌ | ❌ | ✅ | ✅ |
| HTTP 客户端 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 双引擎架构 | ✅ | ✅ | ❌ | ❌ | ✅ | ✅ |
| JS AST 解析 | ✅ | ✅ | ❌ | ❌ | ✅ | ✅ |
| 表单自动填充 | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ |
| WebSocket 监控 | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ |
| GraphQL 发现 | ✅ | ✅ | ❌ | ❌ | ✅ | ✅ |
| Swagger/OpenAPI | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ |
| 历史 URL 发现 | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| 代理池轮换 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 作用域控制 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 路径爬升 | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ |
| 框架检测 | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ |
| 增量爬取 | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ |
| 自适应限速 | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ |
| 反检测 | ✅ | ✅ | ❌ | ❌ | ✅ | ✅ |
| 敏感信息检测 | ✅ | ✅ | ❌ | ❌ | ✅ | ✅ |
| 免费开源 | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ |
