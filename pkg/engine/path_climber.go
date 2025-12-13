// Package engine provides path climbing URL discovery
// P4优化: 路径爬升 - 从深层路径向上爬升发现更多端点
package engine

import (
	"net/url"
	"strings"
	"sync"
)

// PathClimberConfig 路径爬升配置
type PathClimberConfig struct {
	Enabled       bool `yaml:"enabled" json:"enabled"`
	MaxClimbDepth int  `yaml:"max_climb_depth" json:"max_climb_depth"` // 最大爬升层数
}

// DefaultPathClimberConfig 默认配置
func DefaultPathClimberConfig() *PathClimberConfig {
	return &PathClimberConfig{
		Enabled:       true,
		MaxClimbDepth: 5,
	}
}

// PathClimber 路径爬升器
type PathClimber struct {
	config    *PathClimberConfig
	mu        sync.RWMutex
	generated map[string]bool // 已生成的路径
	stats     PathClimberStats
}

// PathClimberStats 统计信息
type PathClimberStats struct {
	TotalProcessed int `json:"total_processed"`
	TotalGenerated int `json:"total_generated"`
	UniqueURLs     int `json:"unique_urls"`
}

// NewPathClimber 创建路径爬升器
func NewPathClimber(config *PathClimberConfig) *PathClimber {
	if config == nil {
		config = DefaultPathClimberConfig()
	}

	return &PathClimber{
		config:    config,
		generated: make(map[string]bool),
	}
}

// ClimbPath 从 URL 生成父路径
// 示例: /api/v1/users/123/profile -> [/api/v1/users/123, /api/v1/users, /api/v1, /api]
func (pc *PathClimber) ClimbPath(urlStr string) []string {
	if !pc.config.Enabled {
		return nil
	}

	u, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}

	pc.mu.Lock()
	pc.stats.TotalProcessed++
	pc.mu.Unlock()

	baseURL := u.Scheme + "://" + u.Host
	path := u.Path

	// 清理路径
	path = strings.TrimSuffix(path, "/")
	if path == "" || path == "/" {
		return nil
	}

	var results []string
	segments := strings.Split(path, "/")

	// 从当前路径向上爬升
	climbCount := 0
	for i := len(segments) - 1; i > 0 && climbCount < pc.config.MaxClimbDepth; i-- {
		parentPath := strings.Join(segments[:i], "/")
		if parentPath == "" {
			parentPath = "/"
		}

		parentURL := baseURL + parentPath

		pc.mu.Lock()
		if !pc.generated[parentURL] {
			pc.generated[parentURL] = true
			results = append(results, parentURL)
			pc.stats.TotalGenerated++
			pc.stats.UniqueURLs++
		}
		pc.mu.Unlock()

		climbCount++
	}

	return results
}

// ClimbPathWithVariants 生成路径变体
// 不仅向上爬升，还尝试常见的变体
func (pc *PathClimber) ClimbPathWithVariants(urlStr string) []string {
	results := pc.ClimbPath(urlStr)

	u, err := url.Parse(urlStr)
	if err != nil {
		return results
	}

	baseURL := u.Scheme + "://" + u.Host
	path := u.Path

	// 尝试常见的 API 版本变体
	apiVersions := []string{"/v1", "/v2", "/v3", "/api/v1", "/api/v2", "/api/v3"}

	// 提取 API 路径部分
	for _, version := range apiVersions {
		if strings.Contains(path, version) {
			// 尝试其他版本
			for _, otherVersion := range apiVersions {
				if version != otherVersion {
					newPath := strings.Replace(path, version, otherVersion, 1)
					newURL := baseURL + newPath

					pc.mu.Lock()
					if !pc.generated[newURL] {
						pc.generated[newURL] = true
						results = append(results, newURL)
						pc.stats.TotalGenerated++
					}
					pc.mu.Unlock()
				}
			}
			break
		}
	}

	// 尝试常见的端点后缀
	commonSuffixes := []string{
		"/", "/index", "/index.html", "/index.php",
		"/api", "/api/", "/graphql", "/swagger.json", "/openapi.json",
		"/health", "/status", "/ping", "/version", "/info",
	}

	// 只对根路径和一级路径添加后缀
	pathDepth := strings.Count(strings.Trim(path, "/"), "/")
	if pathDepth <= 1 {
		for _, suffix := range commonSuffixes {
			cleanPath := strings.TrimSuffix(path, "/")
			newURL := baseURL + cleanPath + suffix

			pc.mu.Lock()
			if !pc.generated[newURL] {
				pc.generated[newURL] = true
				results = append(results, newURL)
				pc.stats.TotalGenerated++
			}
			pc.mu.Unlock()
		}
	}

	return results
}

// GenerateParameterVariants 生成参数变体
// 对带参数的 URL 生成不同参数值的变体
func (pc *PathClimber) GenerateParameterVariants(urlStr string) []string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}

	var results []string
	baseURL := u.Scheme + "://" + u.Host + u.Path

	// 解析现有参数
	query := u.Query()
	if len(query) == 0 {
		return nil
	}

	// 对数字参数尝试常见值
	numericValues := []string{"0", "1", "100", "-1", "999999"}

	for key, values := range query {
		if len(values) == 0 {
			continue
		}

		// 检查是否是数字参数
		isNumeric := isNumericString(values[0])

		if isNumeric {
			for _, testVal := range numericValues {
				newQuery := url.Values{}
				for k, v := range query {
					if k == key {
						newQuery.Set(k, testVal)
					} else {
						for _, val := range v {
							newQuery.Add(k, val)
						}
					}
				}
				newURL := baseURL + "?" + newQuery.Encode()

				pc.mu.Lock()
				if !pc.generated[newURL] {
					pc.generated[newURL] = true
					results = append(results, newURL)
				}
				pc.mu.Unlock()
			}
		}
	}

	// 尝试移除参数
	for key := range query {
		newQuery := url.Values{}
		for k, v := range query {
			if k != key {
				for _, val := range v {
					newQuery.Add(k, val)
				}
			}
		}

		var newURL string
		if len(newQuery) > 0 {
			newURL = baseURL + "?" + newQuery.Encode()
		} else {
			newURL = baseURL
		}

		pc.mu.Lock()
		if !pc.generated[newURL] {
			pc.generated[newURL] = true
			results = append(results, newURL)
		}
		pc.mu.Unlock()
	}

	return results
}

// GetStats 获取统计信息
func (pc *PathClimber) GetStats() PathClimberStats {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.stats
}

// Clear 清空已生成记录
func (pc *PathClimber) Clear() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.generated = make(map[string]bool)
	pc.stats = PathClimberStats{}
}

// HasGenerated 检查是否已生成
func (pc *PathClimber) HasGenerated(urlStr string) bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.generated[urlStr]
}

// isNumericString 检查是否是数字字符串
func isNumericString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c != '-' && c != '.' {
				return false
			}
		}
	}
	return true
}

// ExtractPathSegments 提取路径段
func ExtractPathSegments(urlStr string) []string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}

	path := strings.Trim(u.Path, "/")
	if path == "" {
		return nil
	}

	return strings.Split(path, "/")
}

// GetPathDepth 获取路径深度
func GetPathDepth(urlStr string) int {
	segments := ExtractPathSegments(urlStr)
	return len(segments)
}
