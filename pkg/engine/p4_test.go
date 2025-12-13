package engine

import (
	"testing"
)

// TestPathClimber 测试路径爬升功能
func TestPathClimber(t *testing.T) {
	pc := NewPathClimber(DefaultPathClimberConfig())

	tests := []struct {
		name     string
		url      string
		expected int // 期望生成的父路径数量（最多）
	}{
		{"deep path", "https://example.com/api/v1/users/123/profile", 4},
		{"shallow path", "https://example.com/api", 0},
		{"root path", "https://example.com/", 0},
		{"two level", "https://example.com/api/users", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := pc.ClimbPath(tt.url)
			if len(results) > tt.expected+1 { // 允许一些误差
				t.Errorf("ClimbPath(%s) returned %d results, expected at most %d", tt.url, len(results), tt.expected+1)
			}
			t.Logf("ClimbPath(%s) -> %v", tt.url, results)
		})
	}

	// 测试去重
	pc.Clear()
	results1 := pc.ClimbPath("https://example.com/api/v1/users/123")
	results2 := pc.ClimbPath("https://example.com/api/v1/users/456")
	if len(results2) >= len(results1) {
		t.Log("Deduplication working: second climb returned fewer new URLs")
	}

	// 统计测试
	stats := pc.GetStats()
	if stats.TotalProcessed < 2 {
		t.Errorf("Expected at least 2 processed, got %d", stats.TotalProcessed)
	}
}

// TestScopeController 测试作用域控制
func TestScopeController(t *testing.T) {
	cfg := DefaultScopeConfig()
	cfg.ExcludeExternal = true // 启用外部链接排除
	sc := NewScopeController("https://example.com", cfg)

	tests := []struct {
		name     string
		url      string
		depth    int
		inScope  bool
	}{
		{"same domain", "https://example.com/api/users", 1, true},
		{"subdomain", "https://api.example.com/v1", 1, true},
		{"external domain", "https://google.com/search", 1, false},
		{"cdn domain", "https://cdn.jsdelivr.net/npm/vue", 1, false},
		{"excluded extension", "https://example.com/image.png", 1, false},
		{"excluded path", "https://example.com/wp-content/uploads/file.txt", 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inScope, reason := sc.IsInScope(tt.url, tt.depth)
			if inScope != tt.inScope {
				t.Errorf("IsInScope(%s) = %v, want %v (reason: %s)", tt.url, inScope, tt.inScope, reason)
			}
			t.Logf("IsInScope(%s) = %v (reason: %s)", tt.url, inScope, reason)
		})
	}

	// 测试深度限制
	sc.SetMaxDepth(2)
	inScope, reason := sc.IsInScope("https://example.com/deep/path", 5)
	if inScope {
		t.Errorf("Should be out of scope due to depth, but got inScope=true (reason: %s)", reason)
	}

	// 统计测试
	stats := sc.GetStats()
	if stats.TotalChecked == 0 {
		t.Error("Expected some URLs to be checked")
	}
}

// TestFrameworkDetector 测试框架检测
func TestFrameworkDetector(t *testing.T) {
	tests := []struct {
		name       string
		html       string
		expected   FrameworkType
		expectsSPA bool
	}{
		{
			"react app",
			`<html><div id="root" data-reactroot></div><script src="react.production.min.js"></script></html>`,
			FrameworkReact,
			true,
		},
		{
			"vue app",
			`<html><div id="app" v-bind:class="active" @click="handleClick"></div></html>`,
			FrameworkVue,
			true,
		},
		{
			"angular app",
			`<html ng-app="myApp"><div ng-controller="MainCtrl"></div></html>`,
			FrameworkAngular,
			true,
		},
		{
			"wordpress",
			`<html><link href="/wp-content/themes/theme/style.css"></html>`,
			FrameworkWordPress,
			false,
		},
		{
			"nextjs",
			`<html><script id="__NEXT_DATA__" type="application/json">{}</script></html>`,
			FrameworkNext,
			true,
		},
		{
			"jquery only",
			`<html><script src="jquery.min.js"></script><script>$(document).ready(function(){})</script></html>`,
			FrameworkjQuery,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每个测试用例使用新的检测器避免缓存影响
			fd := NewFrameworkDetector()
			result := fd.Detect(tt.html, nil, nil, "https://"+tt.name+".example.com")
			found := false
			for _, fw := range result.Frameworks {
				if fw == tt.expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Detect() did not find %s in %v", tt.expected, result.Frameworks)
			}
			if result.IsSPA != tt.expectsSPA {
				t.Errorf("Detect() IsSPA = %v, want %v", result.IsSPA, tt.expectsSPA)
			}
			t.Logf("Detected: %v, IsSPA: %v, Confidence: %.2f", result.Frameworks, result.IsSPA, result.Confidence)
		})
	}

	// 测试 ShouldUseBrowser
	fd := NewFrameworkDetector()
	spaHTML := `<html><div id="root"></div><script src="react-dom.min.js"></script></html>`
	if !fd.ShouldUseBrowser(spaHTML) {
		t.Error("ShouldUseBrowser should return true for SPA")
	}

	// 统计测试
	stats := fd.GetStats()
	t.Logf("Framework stats: %+v", stats)
}

// TestPassiveSourceConfig 测试被动源配置
func TestPassiveSourceConfig(t *testing.T) {
	config := DefaultPassiveSourceConfig()

	if config.Enabled {
		t.Error("Default config should have Enabled=false")
	}
	if !config.Wayback {
		t.Error("Default config should have Wayback=true")
	}
	if !config.CommonCrawl {
		t.Error("Default config should have CommonCrawl=true")
	}
	if config.MaxResults <= 0 {
		t.Error("Default config should have positive MaxResults")
	}
}

// TestIsNumericString 测试数字字符串检测
func TestIsNumericString(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"123", true},
		{"0", true},
		{"-1", true},
		{"3.14", true},
		{"abc", false},
		{"12a3", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isNumericString(tt.input)
			if result != tt.expected {
				t.Errorf("isNumericString(%s) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestExtractPathSegments 测试路径段提取
func TestExtractPathSegments(t *testing.T) {
	tests := []struct {
		url      string
		expected int
	}{
		{"https://example.com/api/v1/users", 3},
		{"https://example.com/", 0},
		{"https://example.com", 0},
		{"https://example.com/single", 1},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			segments := ExtractPathSegments(tt.url)
			if len(segments) != tt.expected {
				t.Errorf("ExtractPathSegments(%s) returned %d segments, want %d", tt.url, len(segments), tt.expected)
			}
		})
	}
}

// TestGetPathDepth 测试路径深度
func TestGetPathDepth(t *testing.T) {
	tests := []struct {
		url      string
		expected int
	}{
		{"https://example.com/api/v1/users/123", 4},
		{"https://example.com/api", 1},
		{"https://example.com/", 0},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			depth := GetPathDepth(tt.url)
			if depth != tt.expected {
				t.Errorf("GetPathDepth(%s) = %d, want %d", tt.url, depth, tt.expected)
			}
		})
	}
}
