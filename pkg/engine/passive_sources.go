// Package engine provides passive URL discovery from historical sources
// P4优化: 历史 URL 发现 - Wayback Machine, Common Crawl, VirusTotal, AlienVault
package engine

import (
	"argo/pkg/log"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PassiveSourceConfig 被动源配置
type PassiveSourceConfig struct {
	Enabled           bool          `yaml:"enabled" json:"enabled"`
	Wayback           bool          `yaml:"wayback" json:"wayback"`
	CommonCrawl       bool          `yaml:"common_crawl" json:"common_crawl"`
	VirusTotal        bool          `yaml:"virustotal" json:"virustotal"`
	AlienVault        bool          `yaml:"alienvault" json:"alienvault"`
	URLScan           bool          `yaml:"urlscan" json:"urlscan"`
	Timeout           time.Duration `yaml:"timeout" json:"timeout"`
	MaxResults        int           `yaml:"max_results" json:"max_results"`
	FilterExtensions  []string      `yaml:"filter_extensions" json:"filter_extensions"`
	VirusTotalAPIKey  string        `yaml:"virustotal_api_key" json:"virustotal_api_key"`
	IncludeSubdomains bool          `yaml:"include_subdomains" json:"include_subdomains"`
}

// DefaultPassiveSourceConfig 返回默认配置
func DefaultPassiveSourceConfig() *PassiveSourceConfig {
	return &PassiveSourceConfig{
		Enabled:           false,
		Wayback:           true,
		CommonCrawl:       true,
		VirusTotal:        false, // 需要 API Key
		AlienVault:        true,
		URLScan:           false,
		Timeout:           30 * time.Second,
		MaxResults:        10000,
		IncludeSubdomains: true,
		FilterExtensions: []string{
			".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
			".woff", ".woff2", ".ttf", ".eot", ".otf",
			".mp4", ".mp3", ".avi", ".mov", ".wmv", ".flv",
			".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
			".zip", ".rar", ".7z", ".tar", ".gz",
			".css",
		},
	}
}

// PassiveSourceDiscoverer 被动源发现器
type PassiveSourceDiscoverer struct {
	config *PassiveSourceConfig
	client *http.Client
	mu     sync.RWMutex
	urls   map[string]*PassiveURL // 去重
	stats  PassiveSourceStats
	engine *EngineInfo
}

// PassiveURL 发现的 URL
type PassiveURL struct {
	URL       string    `json:"url"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// PassiveSourceStats 统计信息
type PassiveSourceStats struct {
	WaybackURLs     int `json:"wayback_urls"`
	CommonCrawlURLs int `json:"common_crawl_urls"`
	VirusTotalURLs  int `json:"virustotal_urls"`
	AlienVaultURLs  int `json:"alienvault_urls"`
	URLScanURLs     int `json:"urlscan_urls"`
	TotalDiscovered int `json:"total_discovered"`
	TotalFiltered   int `json:"total_filtered"`
	TotalUnique     int `json:"total_unique"`
}

// NewPassiveSourceDiscoverer 创建被动源发现器
func NewPassiveSourceDiscoverer(engine *EngineInfo, config *PassiveSourceConfig) *PassiveSourceDiscoverer {
	if config == nil {
		config = DefaultPassiveSourceConfig()
	}

	return &PassiveSourceDiscoverer{
		config: config,
		client: &http.Client{Timeout: config.Timeout},
		urls:   make(map[string]*PassiveURL),
		engine: engine,
	}
}

// DiscoverFromDomain 从域名发现历史 URL
func (psd *PassiveSourceDiscoverer) DiscoverFromDomain(domain string) []*PassiveURL {
	if !psd.config.Enabled {
		return nil
	}

	// 清理域名
	domain = psd.cleanDomain(domain)
	if domain == "" {
		return nil
	}

	log.Logger.Infof("[passive] starting discovery for domain: %s", domain)

	var wg sync.WaitGroup
	resultChan := make(chan []*PassiveURL, 5)

	// 并发查询各数据源
	if psd.config.Wayback {
		wg.Add(1)
		go func() {
			defer wg.Done()
			urls := psd.queryWayback(domain)
			resultChan <- urls
		}()
	}

	if psd.config.CommonCrawl {
		wg.Add(1)
		go func() {
			defer wg.Done()
			urls := psd.queryCommonCrawl(domain)
			resultChan <- urls
		}()
	}

	if psd.config.AlienVault {
		wg.Add(1)
		go func() {
			defer wg.Done()
			urls := psd.queryAlienVault(domain)
			resultChan <- urls
		}()
	}

	if psd.config.VirusTotal && psd.config.VirusTotalAPIKey != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			urls := psd.queryVirusTotal(domain)
			resultChan <- urls
		}()
	}

	if psd.config.URLScan {
		wg.Add(1)
		go func() {
			defer wg.Done()
			urls := psd.queryURLScan(domain)
			resultChan <- urls
		}()
	}

	// 等待所有查询完成
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 收集结果
	for urls := range resultChan {
		for _, u := range urls {
			psd.addURL(u)
		}
	}

	return psd.GetURLs()
}

// queryWayback 查询 Wayback Machine
func (psd *PassiveSourceDiscoverer) queryWayback(domain string) []*PassiveURL {
	var results []*PassiveURL

	// Wayback CDX API
	apiURL := fmt.Sprintf(
		"https://web.archive.org/cdx/search/cdx?url=*.%s/*&output=txt&fl=original&collapse=urlkey",
		domain,
	)

	if !psd.config.IncludeSubdomains {
		apiURL = fmt.Sprintf(
			"https://web.archive.org/cdx/search/cdx?url=%s/*&output=txt&fl=original&collapse=urlkey",
			domain,
		)
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		log.Logger.Debugf("[passive/wayback] request error: %v", err)
		return results
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ArgoBot/1.0)")

	resp, err := psd.client.Do(req)
	if err != nil {
		log.Logger.Debugf("[passive/wayback] query error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Logger.Debugf("[passive/wayback] status code: %d", resp.StatusCode)
		return results
	}

	scanner := bufio.NewScanner(resp.Body)
	count := 0
	for scanner.Scan() && count < psd.config.MaxResults {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if psd.shouldFilter(line) {
			psd.mu.Lock()
			psd.stats.TotalFiltered++
			psd.mu.Unlock()
			continue
		}

		results = append(results, &PassiveURL{
			URL:    line,
			Source: "wayback",
		})
		count++
	}

	psd.mu.Lock()
	psd.stats.WaybackURLs += len(results)
	psd.mu.Unlock()

	log.Logger.Debugf("[passive/wayback] found %d URLs for %s", len(results), domain)
	return results
}

// queryCommonCrawl 查询 Common Crawl
func (psd *PassiveSourceDiscoverer) queryCommonCrawl(domain string) []*PassiveURL {
	var results []*PassiveURL

	// 获取最新的索引
	indexes := []string{
		"CC-MAIN-2024-10",
		"CC-MAIN-2024-18",
		"CC-MAIN-2024-22",
		"CC-MAIN-2024-26",
		"CC-MAIN-2024-30",
	}

	for _, index := range indexes {
		apiURL := fmt.Sprintf(
			"https://index.commoncrawl.org/%s-index?url=*.%s&output=json",
			index, domain,
		)

		if !psd.config.IncludeSubdomains {
			apiURL = fmt.Sprintf(
				"https://index.commoncrawl.org/%s-index?url=%s/*&output=json",
				index, domain,
			)
		}

		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ArgoBot/1.0)")

		resp, err := psd.client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			continue
		}

		scanner := bufio.NewScanner(resp.Body)
		count := 0
		for scanner.Scan() && count < psd.config.MaxResults/len(indexes) {
			line := scanner.Text()
			var item struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal([]byte(line), &item); err != nil {
				continue
			}

			if item.URL == "" || psd.shouldFilter(item.URL) {
				psd.mu.Lock()
				psd.stats.TotalFiltered++
				psd.mu.Unlock()
				continue
			}

			results = append(results, &PassiveURL{
				URL:    item.URL,
				Source: "commoncrawl",
			})
			count++
		}
		resp.Body.Close()

		if len(results) > 0 {
			break // 找到结果就停止
		}
	}

	psd.mu.Lock()
	psd.stats.CommonCrawlURLs += len(results)
	psd.mu.Unlock()

	log.Logger.Debugf("[passive/commoncrawl] found %d URLs for %s", len(results), domain)
	return results
}

// queryAlienVault 查询 AlienVault OTX
func (psd *PassiveSourceDiscoverer) queryAlienVault(domain string) []*PassiveURL {
	var results []*PassiveURL

	apiURL := fmt.Sprintf(
		"https://otx.alienvault.com/api/v1/indicators/domain/%s/url_list?limit=%d",
		domain, psd.config.MaxResults,
	)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		log.Logger.Debugf("[passive/alienvault] request error: %v", err)
		return results
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ArgoBot/1.0)")

	resp, err := psd.client.Do(req)
	if err != nil {
		log.Logger.Debugf("[passive/alienvault] query error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Logger.Debugf("[passive/alienvault] status code: %d", resp.StatusCode)
		return results
	}

	var response struct {
		URLList []struct {
			URL  string `json:"url"`
			Date string `json:"date"`
		} `json:"url_list"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return results
	}

	if err := json.Unmarshal(body, &response); err != nil {
		log.Logger.Debugf("[passive/alienvault] parse error: %v", err)
		return results
	}

	for _, item := range response.URLList {
		if item.URL == "" || psd.shouldFilter(item.URL) {
			psd.mu.Lock()
			psd.stats.TotalFiltered++
			psd.mu.Unlock()
			continue
		}

		var timestamp time.Time
		if item.Date != "" {
			timestamp, _ = time.Parse("2006-01-02T15:04:05", item.Date)
		}

		results = append(results, &PassiveURL{
			URL:       item.URL,
			Source:    "alienvault",
			Timestamp: timestamp,
		})
	}

	psd.mu.Lock()
	psd.stats.AlienVaultURLs += len(results)
	psd.mu.Unlock()

	log.Logger.Debugf("[passive/alienvault] found %d URLs for %s", len(results), domain)
	return results
}

// queryVirusTotal 查询 VirusTotal
func (psd *PassiveSourceDiscoverer) queryVirusTotal(domain string) []*PassiveURL {
	var results []*PassiveURL

	if psd.config.VirusTotalAPIKey == "" {
		return results
	}

	apiURL := fmt.Sprintf(
		"https://www.virustotal.com/vtapi/v2/domain/report?apikey=%s&domain=%s",
		psd.config.VirusTotalAPIKey, domain,
	)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return results
	}

	resp, err := psd.client.Do(req)
	if err != nil {
		log.Logger.Debugf("[passive/virustotal] query error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return results
	}

	var response struct {
		DetectedURLs []struct {
			URL string `json:"url"`
		} `json:"detected_urls"`
		UndetectedURLs [][]interface{} `json:"undetected_urls"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return results
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return results
	}

	// detected URLs
	for _, item := range response.DetectedURLs {
		if item.URL == "" || psd.shouldFilter(item.URL) {
			continue
		}
		results = append(results, &PassiveURL{
			URL:    item.URL,
			Source: "virustotal",
		})
	}

	// undetected URLs (format: [url, sha256, positives, total, date])
	for _, item := range response.UndetectedURLs {
		if len(item) > 0 {
			if urlStr, ok := item[0].(string); ok && !psd.shouldFilter(urlStr) {
				results = append(results, &PassiveURL{
					URL:    urlStr,
					Source: "virustotal",
				})
			}
		}
	}

	psd.mu.Lock()
	psd.stats.VirusTotalURLs += len(results)
	psd.mu.Unlock()

	log.Logger.Debugf("[passive/virustotal] found %d URLs for %s", len(results), domain)
	return results
}

// queryURLScan 查询 URLScan.io
func (psd *PassiveSourceDiscoverer) queryURLScan(domain string) []*PassiveURL {
	var results []*PassiveURL

	apiURL := fmt.Sprintf(
		"https://urlscan.io/api/v1/search/?q=domain:%s&size=%d",
		domain, psd.config.MaxResults,
	)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return results
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ArgoBot/1.0)")

	resp, err := psd.client.Do(req)
	if err != nil {
		log.Logger.Debugf("[passive/urlscan] query error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return results
	}

	var response struct {
		Results []struct {
			Page struct {
				URL string `json:"url"`
			} `json:"page"`
		} `json:"results"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return results
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return results
	}

	for _, item := range response.Results {
		if item.Page.URL == "" || psd.shouldFilter(item.Page.URL) {
			continue
		}
		results = append(results, &PassiveURL{
			URL:    item.Page.URL,
			Source: "urlscan",
		})
	}

	psd.mu.Lock()
	psd.stats.URLScanURLs += len(results)
	psd.mu.Unlock()

	log.Logger.Debugf("[passive/urlscan] found %d URLs for %s", len(results), domain)
	return results
}

// shouldFilter 检查是否应该过滤
func (psd *PassiveSourceDiscoverer) shouldFilter(urlStr string) bool {
	lower := strings.ToLower(urlStr)

	// 过滤扩展名
	for _, ext := range psd.config.FilterExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	// 过滤常见无用路径
	filterPatterns := []string{
		"/wp-content/uploads/",
		"/wp-includes/",
		"/static/images/",
		"/assets/images/",
		"/img/",
		"/images/",
		"/fonts/",
		"/media/",
	}

	for _, pattern := range filterPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}

	return false
}

// cleanDomain 清理域名
func (psd *PassiveSourceDiscoverer) cleanDomain(target string) string {
	// 解析 URL
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		u, err := url.Parse(target)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}

	// 移除端口
	if idx := strings.Index(target, ":"); idx != -1 {
		target = target[:idx]
	}

	return target
}

// addURL 添加 URL (去重)
func (psd *PassiveSourceDiscoverer) addURL(u *PassiveURL) {
	if u == nil || u.URL == "" {
		return
	}

	// 规范化 URL
	normalized := psd.normalizeURL(u.URL)
	if normalized == "" {
		return
	}

	psd.mu.Lock()
	defer psd.mu.Unlock()

	if _, exists := psd.urls[normalized]; !exists {
		psd.urls[normalized] = u
		psd.stats.TotalDiscovered++
	}
}

// normalizeURL 规范化 URL
func (psd *PassiveSourceDiscoverer) normalizeURL(urlStr string) string {
	// 移除 fragment
	if idx := strings.Index(urlStr, "#"); idx != -1 {
		urlStr = urlStr[:idx]
	}

	// 解析并重建
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	// 移除常见跟踪参数
	trackingParams := []string{
		"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content",
		"fbclid", "gclid", "ref", "source", "mc_cid", "mc_eid",
	}

	q := u.Query()
	for _, param := range trackingParams {
		q.Del(param)
	}
	u.RawQuery = q.Encode()

	return u.String()
}

// GetURLs 获取所有 URL
func (psd *PassiveSourceDiscoverer) GetURLs() []*PassiveURL {
	psd.mu.RLock()
	defer psd.mu.RUnlock()

	result := make([]*PassiveURL, 0, len(psd.urls))
	for _, u := range psd.urls {
		result = append(result, u)
	}

	psd.stats.TotalUnique = len(result)
	return result
}

// GetStats 获取统计信息
func (psd *PassiveSourceDiscoverer) GetStats() PassiveSourceStats {
	psd.mu.RLock()
	defer psd.mu.RUnlock()

	psd.stats.TotalUnique = len(psd.urls)
	return psd.stats
}

// ExportForCrawler 导出为爬虫格式
func (psd *PassiveSourceDiscoverer) ExportForCrawler() []*UrlInfo {
	psd.mu.RLock()
	defer psd.mu.RUnlock()

	result := make([]*UrlInfo, 0, len(psd.urls))
	for _, u := range psd.urls {
		result = append(result, &UrlInfo{
			Url:        u.URL,
			SourceType: "passive:" + u.Source,
			SourceUrl:  u.Source,
			Depth:      0,
		})
	}

	return result
}

// Clear 清空数据
func (psd *PassiveSourceDiscoverer) Clear() {
	psd.mu.Lock()
	defer psd.mu.Unlock()

	psd.urls = make(map[string]*PassiveURL)
	psd.stats = PassiveSourceStats{}
}

// FilterByPattern 按正则过滤
func (psd *PassiveSourceDiscoverer) FilterByPattern(pattern string) []*PassiveURL {
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}

	psd.mu.RLock()
	defer psd.mu.RUnlock()

	result := make([]*PassiveURL, 0)
	for _, u := range psd.urls {
		if regex.MatchString(u.URL) {
			result = append(result, u)
		}
	}

	return result
}

// FilterByExtension 按扩展名过滤
func (psd *PassiveSourceDiscoverer) FilterByExtension(extensions []string) []*PassiveURL {
	psd.mu.RLock()
	defer psd.mu.RUnlock()

	result := make([]*PassiveURL, 0)
	for _, u := range psd.urls {
		lower := strings.ToLower(u.URL)
		match := false
		for _, ext := range extensions {
			if strings.HasSuffix(lower, ext) {
				match = true
				break
			}
		}
		if match {
			result = append(result, u)
		}
	}

	return result
}

// GetJSFiles 获取 JS 文件
func (psd *PassiveSourceDiscoverer) GetJSFiles() []*PassiveURL {
	return psd.FilterByExtension([]string{".js", ".mjs", ".jsx", ".ts", ".tsx"})
}

// GetAPIEndpoints 获取可能的 API 端点
func (psd *PassiveSourceDiscoverer) GetAPIEndpoints() []*PassiveURL {
	return psd.FilterByPattern(`(?i)(/api/|/v\d+/|/rest/|/graphql|\.json$)`)
}
