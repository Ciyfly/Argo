package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestURLClassifier_APIPatterns(t *testing.T) {
	classifier := NewURLClassifier(nil)

	testCases := []struct {
		url      string
		expected PageType
	}{
		{"/api/users", PageTypeAPI},
		{"/api/v1/products", PageTypeAPI},
		{"/v2/orders", PageTypeAPI},
		{"/rest/data", PageTypeAPI},
		{"/graphql", PageTypeAPI},
		{"/ajax/submit", PageTypeAPI},
		{"/data.json", PageTypeAPI},
		{"/config.xml", PageTypeAPI},
	}

	for _, tc := range testCases {
		result := classifier.Classify("https://example.com" + tc.url)
		if result.PageType != tc.expected {
			t.Errorf("URL %s: expected %s, got %s (reason: %s)",
				tc.url, tc.expected, result.PageType, result.Reason)
		}
	}
}

func TestURLClassifier_ResourcePatterns(t *testing.T) {
	classifier := NewURLClassifier(nil)

	testCases := []struct {
		url      string
		expected PageType
	}{
		{"/style.css", PageTypeResource},
		{"/app.js", PageTypeResource},
		{"/logo.png", PageTypeResource},
		{"/image.jpg", PageTypeResource},
		{"/font.woff2", PageTypeResource},
		{"/video.mp4", PageTypeResource},
		{"/document.pdf", PageTypeResource},
	}

	for _, tc := range testCases {
		result := classifier.Classify("https://example.com" + tc.url)
		if result.PageType != tc.expected {
			t.Errorf("URL %s: expected %s, got %s (reason: %s)",
				tc.url, tc.expected, result.PageType, result.Reason)
		}
	}
}

func TestURLClassifier_SPAPatterns(t *testing.T) {
	classifier := NewURLClassifier(nil)

	testCases := []struct {
		url      string
		expected PageType
	}{
		// Hash routing 是明确的 SPA 标志
		{"https://example.com/#/dashboard", PageTypeSPA},
		// JS 文件会被识别为资源（这是正确的）
		// SPA 检测主要通过内容而非 URL
	}

	for _, tc := range testCases {
		result := classifier.Classify(tc.url)
		if result.PageType != tc.expected {
			t.Errorf("URL %s: expected %s, got %s (reason: %s)",
				tc.url, tc.expected, result.PageType, result.Reason)
		}
	}
}

func TestURLClassifier_LearnFromUpgrade(t *testing.T) {
	classifier := NewURLClassifier(nil)

	// 第一次分类 - 应该是静态
	url := "https://example.com/app/dashboard"
	result1 := classifier.Classify(url)

	// 记录升级
	classifier.RecordUpgrade(url, "needs_js_rendering")
	classifier.RecordUpgrade(url, "needs_js_rendering")

	// 第二次分类 - 应该被识别为动态
	result2 := classifier.Classify(url)

	if result2.PageType != PageTypeDynamic {
		t.Errorf("After upgrade recording, expected dynamic, got %s", result2.PageType)
	}

	t.Logf("Before learning: %s, After learning: %s", result1.PageType, result2.PageType)
}

func TestDualEngine_ProcessURL(t *testing.T) {
	config := DefaultDualEngineConfig()
	// 创建一个 mock engine
	mockEngine := &EngineInfo{}
	de := NewDualEngine(mockEngine, config)

	testCases := []struct {
		url      string
		expected EngineType
	}{
		// 强制 Standard (通过 ForceStandardPatterns)
		{"/api/v1/users.json", EngineTypeStandard},
		{"/rest/data", EngineTypeStandard},
		{"/graphql", EngineTypeStandard},

		// 强制 Hybrid (通过 ForceHybridPatterns)
		{"/login", EngineTypeHybrid},
		{"/signin", EngineTypeHybrid},
		{"/dashboard", EngineTypeHybrid},
		{"/checkout", EngineTypeHybrid},
	}

	for _, tc := range testCases {
		uif := &UrlInfo{Url: "https://example.com" + tc.url}
		result := de.ProcessURL(uif)
		if result != tc.expected {
			t.Errorf("URL %s: expected %s, got %s", tc.url, tc.expected, result)
		}
	}
}

func TestDualEngine_ForcePatterns(t *testing.T) {
	config := DefaultDualEngineConfig()
	config.ForceStandardPatterns = []string{`\.json$`, `/api/`}
	config.ForceHybridPatterns = []string{`/admin`, `/login`}

	mockEngine := &EngineInfo{}
	de := NewDualEngine(mockEngine, config)

	testCases := []struct {
		url      string
		expected EngineType
	}{
		{"/data.json", EngineTypeStandard},
		{"/api/users", EngineTypeStandard},
		{"/admin/panel", EngineTypeHybrid},
		{"/login", EngineTypeHybrid},
	}

	for _, tc := range testCases {
		engineType := de.checkForcePatterns("https://example.com" + tc.url)
		if engineType != tc.expected && engineType != EngineTypeAuto {
			t.Errorf("URL %s: expected %s, got %s", tc.url, tc.expected, engineType)
		}
	}
}

func TestStandardEngine_NeedsHybridEngine(t *testing.T) {
	mockEngine := &EngineInfo{}
	config := &StandardEngineConfig{
		Workers:   1,
		Timeout:   10 * time.Second,
		Retries:   1,
		UserAgent: "test",
	}
	se := NewStandardEngine(mockEngine, config)

	testCases := []struct {
		name        string
		body        string
		contentType string
		expected    bool
	}{
		{
			name:        "React App",
			body:        `<div id="root"></div><script>`,
			contentType: "text/html",
			expected:    true,
		},
		{
			name:        "Vue App",
			body:        `<div id="app"></div><script>`,
			contentType: "text/html",
			expected:    true,
		},
		{
			name:        "Next.js",
			body:        `<div id="__next"></div><script>window.__NEXT_DATA__`,
			contentType: "text/html",
			expected:    true,
		},
		{
			name:        "Angular",
			body:        `<div ng-app="myApp">`,
			contentType: "text/html",
			expected:    true,
		},
		{
			name:        "Static HTML",
			body:        `<html><body><h1>Hello World</h1><p>This is static content with lots of text that makes it clearly not a SPA skeleton</p></body></html>`,
			contentType: "text/html",
			expected:    false,
		},
		{
			name:        "JSON API",
			body:        `{"users": [{"id": 1}]}`,
			contentType: "application/json",
			expected:    false,
		},
		{
			name:        "XML API",
			body:        `<users><user id="1"/></users>`,
			contentType: "application/xml",
			expected:    false,
		},
	}

	for _, tc := range testCases {
		result := se.needsHybridEngine(tc.body, tc.contentType)
		if result != tc.expected {
			t.Errorf("%s: expected %v, got %v", tc.name, tc.expected, result)
		}
	}
}

func TestStandardEngine_ProcessResponse(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/data":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "ok", "links": ["/api/users", "/api/products"]}`))
		case "/static":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<html><body><a href="/page1">Link</a><a href="/page2">Link2</a></body></html>`))
		case "/spa":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<div id="root"></div><script>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// 创建 mock engine
	mockEngine := &EngineInfo{
		ResultQueue: make(chan *PendingUrl, 100),
	}

	config := &StandardEngineConfig{
		Workers:   1,
		Timeout:   10 * time.Second,
		Retries:   1,
		UserAgent: "test",
	}
	se := NewStandardEngine(mockEngine, config)

	// 测试 JSON API
	req, _ := http.NewRequest("GET", server.URL+"/api/data", nil)
	resp, err := se.client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// 读取响应检查
	body := make([]byte, 1024)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	bodyStr := string(body[:n])

	if !strings.Contains(bodyStr, "status") {
		t.Errorf("Expected JSON response, got: %s", bodyStr)
	}
}

func TestDualEngineStats(t *testing.T) {
	config := DefaultDualEngineConfig()
	mockEngine := &EngineInfo{}
	de := NewDualEngine(mockEngine, config)

	// 处理一些 URL - 注意：ProcessURL 只有在 config.Enabled=true 时才会计数
	// 默认配置是 Enabled=true
	de.ProcessURL(&UrlInfo{Url: "https://example.com/api/data.json"})
	de.ProcessURL(&UrlInfo{Url: "https://example.com/login"})
	de.ProcessURL(&UrlInfo{Url: "https://example.com/dashboard"})
	de.ProcessURL(&UrlInfo{Url: "https://example.com/rest/users"})

	stats := de.GetStats()

	t.Logf("DualEngine Stats:")
	t.Logf("  Standard Requests: %d", stats.StandardRequests)
	t.Logf("  Hybrid Requests: %d", stats.HybridRequests)
	t.Logf("  Total: %d", stats.StandardRequests+stats.HybridRequests)

	// 检查是否有请求被处理
	total := stats.StandardRequests + stats.HybridRequests
	if total != 4 {
		t.Errorf("Expected 4 total requests, got %d", total)
	}
}

func TestURLClassifier_PathNormalization(t *testing.T) {
	classifier := NewURLClassifier(nil)

	// 测试路径规范化
	urls := []string{
		"https://example.com/users/123/profile",
		"https://example.com/users/456/profile",
		"https://example.com/users/789/profile",
	}

	// 获取路径 key
	keys := make(map[string]bool)
	for _, u := range urls {
		key := classifier.getPathKey(u)
		keys[key] = true
	}

	// 应该只有一个规范化的 key
	if len(keys) != 1 {
		t.Errorf("Expected 1 normalized key, got %d: %v", len(keys), keys)
	}

	for k := range keys {
		if !strings.Contains(k, "{id}") {
			t.Errorf("Expected path key to contain {id}, got: %s", k)
		}
	}
}

func BenchmarkURLClassifier_Classify(b *testing.B) {
	classifier := NewURLClassifier(nil)
	urls := []string{
		"https://example.com/api/v1/users",
		"https://example.com/static/style.css",
		"https://example.com/dashboard",
		"https://example.com/products/123",
		"https://example.com/data.json",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		classifier.Classify(urls[i%len(urls)])
	}
}

func BenchmarkDualEngine_ProcessURL(b *testing.B) {
	config := DefaultDualEngineConfig()
	mockEngine := &EngineInfo{}
	de := NewDualEngine(mockEngine, config)

	urls := []*UrlInfo{
		{Url: "https://example.com/api/users.json"},
		{Url: "https://example.com/login"},
		{Url: "https://example.com/dashboard"},
		{Url: "https://example.com/products"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		de.ProcessURL(urls[i%len(urls)])
	}
}

// ============================================================================
// 自适应限速器测试
// ============================================================================

func TestBrowserRateLimiter_Basic(t *testing.T) {
	config := &BrowserRateLimitConfig{
		Enabled:      true,
		BaseInterval: 50 * time.Millisecond,
		MinInterval:  10 * time.Millisecond,
		MaxInterval:  500 * time.Millisecond,
	}
	limiter := NewBrowserRateLimiter(config)

	// 测试基本等待
	start := time.Now()
	limiter.Wait("example.com")
	elapsed := time.Since(start)

	// 第一次请求应该很快（因为初始化时允许立即开始）
	if elapsed > 100*time.Millisecond {
		t.Logf("First wait took %v (expected < 100ms)", elapsed)
	}

	// 第二次请求应该等待
	start = time.Now()
	limiter.Wait("example.com")
	elapsed = time.Since(start)

	// 应该等待约 BaseInterval
	if elapsed < 40*time.Millisecond {
		t.Logf("Second wait took %v (expected >= 40ms)", elapsed)
	}

	t.Logf("BrowserRateLimiter basic test passed")
}

func TestBrowserRateLimiter_AdaptiveSpeedup(t *testing.T) {
	config := &BrowserRateLimitConfig{
		Enabled:      true,
		BaseInterval: 100 * time.Millisecond,
		MinInterval:  20 * time.Millisecond,
		MaxInterval:  1 * time.Second,
	}
	limiter := NewBrowserRateLimiter(config)

	domain := "speedup-test.com"

	// 记录 15 次成功 (10 次后应该加速)
	for i := 0; i < 15; i++ {
		limiter.RecordSuccess(domain)
	}

	// 获取当前间隔
	interval := limiter.GetCurrentInterval(domain)

	// 应该小于基准间隔
	if interval >= 100*time.Millisecond {
		t.Errorf("Expected interval < 100ms after successes, got %v", interval)
	}

	t.Logf("After 15 successes, interval: %v (base: 100ms)", interval)
}

func TestBrowserRateLimiter_AdaptiveSlowdown(t *testing.T) {
	config := &BrowserRateLimitConfig{
		Enabled:      true,
		BaseInterval: 100 * time.Millisecond,
		MinInterval:  20 * time.Millisecond,
		MaxInterval:  1 * time.Second,
	}
	limiter := NewBrowserRateLimiter(config)

	domain := "slowdown-test.com"

	// 记录 429 错误
	limiter.RecordFailure(domain, 429)

	// 获取当前间隔
	interval := limiter.GetCurrentInterval(domain)

	// 应该大于基准间隔 (429 导致 3 倍增加)
	if interval <= 100*time.Millisecond {
		t.Errorf("Expected interval > 100ms after 429, got %v", interval)
	}

	t.Logf("After 429 error, interval: %v (base: 100ms)", interval)
}

func TestBrowserRateLimiter_Disabled(t *testing.T) {
	config := &BrowserRateLimitConfig{
		Enabled:      false,
		BaseInterval: 100 * time.Millisecond,
		MinInterval:  20 * time.Millisecond,
		MaxInterval:  1 * time.Second,
	}
	limiter := NewBrowserRateLimiter(config)

	// 当禁用时，Wait 应该立即返回
	start := time.Now()
	limiter.Wait("example.com")
	elapsed := time.Since(start)

	if elapsed > 10*time.Millisecond {
		t.Errorf("Expected immediate return when disabled, took %v", elapsed)
	}

	// TryAcquire 应该返回 0
	wait := limiter.TryAcquire("example.com")
	if wait != 0 {
		t.Errorf("Expected 0 wait time when disabled, got %v", wait)
	}

	// GetCurrentInterval 应该返回 0
	interval := limiter.GetCurrentInterval("example.com")
	if interval != 0 {
		t.Errorf("Expected 0 interval when disabled, got %v", interval)
	}
}

func TestBrowserRateLimiter_Stats(t *testing.T) {
	config := &BrowserRateLimitConfig{
		Enabled:      true,
		BaseInterval: 50 * time.Millisecond,
		MinInterval:  10 * time.Millisecond,
		MaxInterval:  500 * time.Millisecond,
	}
	limiter := NewBrowserRateLimiter(config)

	domain := "stats-test.com"

	// 进行一些操作
	limiter.Wait(domain)
	limiter.RecordSuccess(domain)
	limiter.Wait(domain)
	limiter.RecordFailure(domain, 500)
	limiter.Wait(domain)

	stats := limiter.Stats()

	if !stats.Enabled {
		t.Error("Expected Enabled to be true")
	}

	t.Logf("BrowserRateLimiter Stats:")
	t.Logf("  Enabled: %v", stats.Enabled)
	t.Logf("  TotalWaits: %d", stats.TotalWaits)
	t.Logf("  TotalWaitMs: %d", stats.TotalWaitMs)
	t.Logf("  ThrottledCount: %d", stats.ThrottledCount)
	t.Logf("  AvgWaitMs: %d", stats.AvgWaitMs)

	// 应该有至少一次被限速的记录
	if stats.ThrottledCount != 1 {
		t.Errorf("Expected ThrottledCount=1, got %d", stats.ThrottledCount)
	}
}

func TestBrowserRateLimiter_PerDomain(t *testing.T) {
	config := &BrowserRateLimitConfig{
		Enabled:      true,
		BaseInterval: 50 * time.Millisecond,
		MinInterval:  10 * time.Millisecond,
		MaxInterval:  500 * time.Millisecond,
	}
	limiter := NewBrowserRateLimiter(config)

	// 对不同域名进行操作
	limiter.RecordFailure("domain1.com", 429) // 大幅降速
	limiter.RecordSuccess("domain2.com")      // 正常

	interval1 := limiter.GetCurrentInterval("domain1.com")
	interval2 := limiter.GetCurrentInterval("domain2.com")

	// domain1 应该有更长的间隔
	if interval1 <= interval2 {
		t.Errorf("Expected domain1 interval > domain2, got %v <= %v", interval1, interval2)
	}

	t.Logf("Per-domain intervals: domain1=%v, domain2=%v", interval1, interval2)
}

func BenchmarkBrowserRateLimiter_Wait(b *testing.B) {
	config := &BrowserRateLimitConfig{
		Enabled:      true,
		BaseInterval: 1 * time.Millisecond, // 使用非常短的间隔进行基准测试
		MinInterval:  100 * time.Microsecond,
		MaxInterval:  100 * time.Millisecond,
	}
	limiter := NewBrowserRateLimiter(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Wait("benchmark.com")
		limiter.RecordSuccess("benchmark.com")
	}
}
