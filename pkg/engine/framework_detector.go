// Package engine provides frontend framework fingerprinting
// P4优化: 框架指纹识别 - 识别前端框架以优化爬取策略
package engine

import (
	"regexp"
	"strings"
	"sync"
)

// FrameworkType 框架类型
type FrameworkType string

const (
	FrameworkReact     FrameworkType = "react"
	FrameworkVue       FrameworkType = "vue"
	FrameworkAngular   FrameworkType = "angular"
	FrameworkNext      FrameworkType = "nextjs"
	FrameworkNuxt      FrameworkType = "nuxtjs"
	FrameworkSvelte    FrameworkType = "svelte"
	FrameworkEmber     FrameworkType = "ember"
	FrameworkBackbone  FrameworkType = "backbone"
	FrameworkjQuery    FrameworkType = "jquery"
	FrameworkWordPress FrameworkType = "wordpress"
	FrameworkDrupal    FrameworkType = "drupal"
	FrameworkLaravel   FrameworkType = "laravel"
	FrameworkDjango    FrameworkType = "django"
	FrameworkRails     FrameworkType = "rails"
	FrameworkSpring    FrameworkType = "spring"
	FrameworkExpress   FrameworkType = "express"
	FrameworkUnknown   FrameworkType = "unknown"
)

// FrameworkFingerprint 框架指纹
type FrameworkFingerprint struct {
	Name          FrameworkType
	Patterns      []*regexp.Regexp
	HeaderPatterns map[string]*regexp.Regexp
	CookiePatterns []string
	PathPatterns  []string
	IsSPA         bool
	NeedsBrowser  bool
}

// FrameworkDetector 框架检测器
type FrameworkDetector struct {
	fingerprints []*FrameworkFingerprint
	mu           sync.RWMutex
	cache        map[string]*FrameworkResult
	stats        FrameworkStats
}

// FrameworkResult 检测结果
type FrameworkResult struct {
	Frameworks    []FrameworkType `json:"frameworks"`
	IsSPA         bool            `json:"is_spa"`
	NeedsBrowser  bool            `json:"needs_browser"`
	Confidence    float64         `json:"confidence"`
	MatchedRules  []string        `json:"matched_rules"`
	Technologies  []string        `json:"technologies"`
}

// FrameworkStats 统计信息
type FrameworkStats struct {
	TotalDetected int            `json:"total_detected"`
	ByFramework   map[string]int `json:"by_framework"`
	SPACount      int            `json:"spa_count"`
	StaticCount   int            `json:"static_count"`
}

// NewFrameworkDetector 创建框架检测器
func NewFrameworkDetector() *FrameworkDetector {
	fd := &FrameworkDetector{
		cache: make(map[string]*FrameworkResult),
		stats: FrameworkStats{
			ByFramework: make(map[string]int),
		},
	}

	fd.initFingerprints()
	return fd
}

// initFingerprints 初始化指纹库
func (fd *FrameworkDetector) initFingerprints() {
	fd.fingerprints = []*FrameworkFingerprint{
		// React
		{
			Name: FrameworkReact,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)data-reactroot`),
				regexp.MustCompile(`(?i)data-react-`),
				regexp.MustCompile(`(?i)_reactRootContainer`),
				regexp.MustCompile(`(?i)__REACT_DEVTOOLS_GLOBAL_HOOK__`),
				regexp.MustCompile(`(?i)react\.production\.min\.js`),
				regexp.MustCompile(`(?i)react-dom`),
				regexp.MustCompile(`(?i)"react":\s*"\d`),
			},
			IsSPA:        true,
			NeedsBrowser: true,
		},
		// Vue
		{
			Name: FrameworkVue,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)__VUE__`),
				regexp.MustCompile(`(?i)v-bind:`),
				regexp.MustCompile(`(?i)v-on:`),
				regexp.MustCompile(`(?i)v-model`),
				regexp.MustCompile(`(?i)v-if=`),
				regexp.MustCompile(`(?i)v-for=`),
				regexp.MustCompile(`(?i):href=`),
				regexp.MustCompile(`(?i)@click=`),
				regexp.MustCompile(`(?i)vue\.runtime`),
				regexp.MustCompile(`(?i)vue\.min\.js`),
				regexp.MustCompile(`(?i)data-v-[a-f0-9]+`),
			},
			IsSPA:        true,
			NeedsBrowser: true,
		},
		// Angular
		{
			Name: FrameworkAngular,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)ng-version=`),
				regexp.MustCompile(`(?i)ng-app=`),
				regexp.MustCompile(`(?i)ng-controller=`),
				regexp.MustCompile(`(?i)ng-model=`),
				regexp.MustCompile(`(?i)\[\(ngModel\)\]`),
				regexp.MustCompile(`(?i)_ng_`),
				regexp.MustCompile(`(?i)angular\.min\.js`),
				regexp.MustCompile(`(?i)@angular/core`),
				regexp.MustCompile(`(?i)platformBrowserDynamic`),
			},
			IsSPA:        true,
			NeedsBrowser: true,
		},
		// Next.js
		{
			Name: FrameworkNext,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)__NEXT_DATA__`),
				regexp.MustCompile(`(?i)_next/static`),
				regexp.MustCompile(`(?i)next\.config`),
				regexp.MustCompile(`(?i)next/router`),
				regexp.MustCompile(`(?i)NextJS`),
			},
			HeaderPatterns: map[string]*regexp.Regexp{
				"x-powered-by": regexp.MustCompile(`(?i)next\.js`),
			},
			IsSPA:        true,
			NeedsBrowser: true,
		},
		// Nuxt.js
		{
			Name: FrameworkNuxt,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)__NUXT__`),
				regexp.MustCompile(`(?i)_nuxt/`),
				regexp.MustCompile(`(?i)nuxt\.config`),
				regexp.MustCompile(`(?i)nuxt-link`),
			},
			IsSPA:        true,
			NeedsBrowser: true,
		},
		// Svelte
		{
			Name: FrameworkSvelte,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)svelte-`),
				regexp.MustCompile(`(?i)__svelte`),
				regexp.MustCompile(`(?i)svelte\.dev`),
			},
			IsSPA:        true,
			NeedsBrowser: true,
		},
		// Ember
		{
			Name: FrameworkEmber,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)ember\.js`),
				regexp.MustCompile(`(?i)data-ember-`),
				regexp.MustCompile(`(?i)ember-view`),
				regexp.MustCompile(`(?i)Ember\.`),
			},
			IsSPA:        true,
			NeedsBrowser: true,
		},
		// jQuery
		{
			Name: FrameworkjQuery,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)jquery[.-]?\d`),
				regexp.MustCompile(`(?i)jquery\.min\.js`),
				regexp.MustCompile(`(?i)\$\(document\)\.ready`),
				regexp.MustCompile(`(?i)\$\(function`),
			},
			IsSPA:        false,
			NeedsBrowser: false,
		},
		// WordPress
		{
			Name: FrameworkWordPress,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)/wp-content/`),
				regexp.MustCompile(`(?i)/wp-includes/`),
				regexp.MustCompile(`(?i)/wp-admin/`),
				regexp.MustCompile(`(?i)wp-json`),
				regexp.MustCompile(`(?i)WordPress`),
			},
			PathPatterns: []string{"/wp-content/", "/wp-admin/", "/wp-json/"},
			IsSPA:        false,
			NeedsBrowser: false,
		},
		// Drupal
		{
			Name: FrameworkDrupal,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)Drupal\.`),
				regexp.MustCompile(`(?i)/sites/default/`),
				regexp.MustCompile(`(?i)/modules/`),
				regexp.MustCompile(`(?i)drupal\.js`),
			},
			HeaderPatterns: map[string]*regexp.Regexp{
				"x-generator": regexp.MustCompile(`(?i)drupal`),
			},
			IsSPA:        false,
			NeedsBrowser: false,
		},
		// Laravel
		{
			Name: FrameworkLaravel,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)laravel_session`),
				regexp.MustCompile(`(?i)csrf-token.*content=`),
			},
			CookiePatterns: []string{"laravel_session", "XSRF-TOKEN"},
			IsSPA:          false,
			NeedsBrowser:   false,
		},
		// Django
		{
			Name: FrameworkDjango,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)csrfmiddlewaretoken`),
				regexp.MustCompile(`(?i)__admin__`),
			},
			CookiePatterns: []string{"csrftoken", "sessionid"},
			PathPatterns:   []string{"/admin/"},
			IsSPA:          false,
			NeedsBrowser:   false,
		},
		// Rails
		{
			Name: FrameworkRails,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)csrf-token.*content=`),
				regexp.MustCompile(`(?i)data-turbo`),
				regexp.MustCompile(`(?i)turbolinks`),
			},
			CookiePatterns: []string{"_session_id"},
			HeaderPatterns: map[string]*regexp.Regexp{
				"x-powered-by": regexp.MustCompile(`(?i)phusion passenger`),
			},
			IsSPA:        false,
			NeedsBrowser: false,
		},
		// Spring
		{
			Name: FrameworkSpring,
			Patterns: []*regexp.Regexp{
				regexp.MustCompile(`(?i)org\.springframework`),
			},
			CookiePatterns: []string{"JSESSIONID"},
			PathPatterns:   []string{"/actuator/", "/swagger-ui/"},
			IsSPA:          false,
			NeedsBrowser:   false,
		},
	}
}

// Detect 检测框架
func (fd *FrameworkDetector) Detect(html string, headers map[string]string, cookies []string, url string) *FrameworkResult {
	// 检查缓存
	fd.mu.RLock()
	if cached, ok := fd.cache[url]; ok {
		fd.mu.RUnlock()
		return cached
	}
	fd.mu.RUnlock()

	result := &FrameworkResult{
		Frameworks:   make([]FrameworkType, 0),
		MatchedRules: make([]string, 0),
		Technologies: make([]string, 0),
	}

	matchCount := 0

	for _, fp := range fd.fingerprints {
		matched := false
		confidence := 0.0

		// 检查 HTML 模式
		for _, pattern := range fp.Patterns {
			if pattern.MatchString(html) {
				matched = true
				confidence += 0.3
				result.MatchedRules = append(result.MatchedRules, fp.Name.String()+":html_pattern")
			}
		}

		// 检查 Header 模式
		for headerName, pattern := range fp.HeaderPatterns {
			if headerVal, ok := headers[strings.ToLower(headerName)]; ok {
				if pattern.MatchString(headerVal) {
					matched = true
					confidence += 0.4
					result.MatchedRules = append(result.MatchedRules, fp.Name.String()+":header_pattern")
				}
			}
		}

		// 检查 Cookie 模式
		for _, cookiePattern := range fp.CookiePatterns {
			for _, cookie := range cookies {
				if strings.Contains(strings.ToLower(cookie), strings.ToLower(cookiePattern)) {
					matched = true
					confidence += 0.2
					result.MatchedRules = append(result.MatchedRules, fp.Name.String()+":cookie_pattern")
				}
			}
		}

		// 检查路径模式
		for _, pathPattern := range fp.PathPatterns {
			if strings.Contains(url, pathPattern) {
				matched = true
				confidence += 0.2
				result.MatchedRules = append(result.MatchedRules, fp.Name.String()+":path_pattern")
			}
		}

		if matched {
			result.Frameworks = append(result.Frameworks, fp.Name)
			result.Technologies = append(result.Technologies, string(fp.Name))
			matchCount++

			if fp.IsSPA {
				result.IsSPA = true
			}
			if fp.NeedsBrowser {
				result.NeedsBrowser = true
			}

			// 更新置信度
			if confidence > result.Confidence {
				result.Confidence = confidence
			}
		}
	}

	// 如果没有检测到框架，设置为未知
	if len(result.Frameworks) == 0 {
		result.Frameworks = append(result.Frameworks, FrameworkUnknown)

		// 启发式判断是否是 SPA
		spaIndicators := []string{
			`<div id="app">`,
			`<div id="root">`,
			`<script type="module"`,
			`async src=`,
			`defer src=`,
		}

		spaCount := 0
		for _, indicator := range spaIndicators {
			if strings.Contains(html, indicator) {
				spaCount++
			}
		}

		if spaCount >= 2 {
			result.IsSPA = true
			result.NeedsBrowser = true
		}
	}

	// 规范化置信度
	if result.Confidence > 1.0 {
		result.Confidence = 1.0
	}

	// 更新统计
	fd.mu.Lock()
	fd.stats.TotalDetected++
	for _, fw := range result.Frameworks {
		fd.stats.ByFramework[string(fw)]++
	}
	if result.IsSPA {
		fd.stats.SPACount++
	} else {
		fd.stats.StaticCount++
	}
	fd.cache[url] = result
	fd.mu.Unlock()

	return result
}

// String 框架类型转字符串
func (ft FrameworkType) String() string {
	return string(ft)
}

// GetStats 获取统计信息
func (fd *FrameworkDetector) GetStats() FrameworkStats {
	fd.mu.RLock()
	defer fd.mu.RUnlock()

	// 复制 map
	byFramework := make(map[string]int)
	for k, v := range fd.stats.ByFramework {
		byFramework[k] = v
	}

	return FrameworkStats{
		TotalDetected: fd.stats.TotalDetected,
		ByFramework:   byFramework,
		SPACount:      fd.stats.SPACount,
		StaticCount:   fd.stats.StaticCount,
	}
}

// ClearCache 清除缓存
func (fd *FrameworkDetector) ClearCache() {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	fd.cache = make(map[string]*FrameworkResult)
}

// ShouldUseBrowser 判断是否应该使用浏览器
func (fd *FrameworkDetector) ShouldUseBrowser(html string) bool {
	// 快速检查 SPA 指标
	spaPatterns := []string{
		"data-reactroot",
		"__NEXT_DATA__",
		"__NUXT__",
		"ng-version",
		"v-bind:",
		"<div id=\"app\"></div>",
		"<div id=\"root\"></div>",
	}

	for _, pattern := range spaPatterns {
		if strings.Contains(html, pattern) {
			return true
		}
	}

	// 检查是否主要内容在 JS 中
	bodyContentLen := len(extractBodyContent(html))
	if bodyContentLen < 500 && strings.Contains(html, "<script") {
		return true
	}

	return false
}

// extractBodyContent 提取 body 内容（简化版）
func extractBodyContent(html string) string {
	bodyStart := strings.Index(html, "<body")
	if bodyStart == -1 {
		return html
	}

	bodyEnd := strings.Index(html[bodyStart:], ">")
	if bodyEnd == -1 {
		return ""
	}

	bodyClose := strings.Index(html, "</body>")
	if bodyClose == -1 {
		bodyClose = len(html)
	}

	content := html[bodyStart+bodyEnd+1 : bodyClose]

	// 移除 script 和 style 标签
	scriptRe := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRe := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)

	content = scriptRe.ReplaceAllString(content, "")
	content = styleRe.ReplaceAllString(content, "")

	// 移除 HTML 标签
	tagRe := regexp.MustCompile(`<[^>]+>`)
	content = tagRe.ReplaceAllString(content, " ")

	return strings.TrimSpace(content)
}
