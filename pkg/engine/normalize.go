package engine

import (
	"argo/pkg/log"
	"argo/pkg/utils"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// 泛化去重

type PendingUrl struct {
	URL    string
	Method string
	Host   string
	// SourceType/SourceUrl 用于描述该 URL 的“发现来源”
	SourceType      string
	SourceUrl       string
	Headers         http.Header
	Data            string
	Status          int
	ResponseHeaders http.Header
	ResponseBody    string
	RequestStr      string
}

const normalizeCacheLimit = 500000

var (
	uuidRegex      = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hexRegex       = regexp.MustCompile(`(?i)^[0-9a-f]+$`)
	dateRegex      = regexp.MustCompile(`^\d{4}-\d{1,2}-\d{1,2}$`)
	timestampRegex = regexp.MustCompile(`^\d{10,13}$`)                                            // Unix 时间戳
	versionRegex   = regexp.MustCompile(`^v?\d+(\.\d+)*$`)                                        // 版本号
	base64Regex    = regexp.MustCompile(`^[A-Za-z0-9+/]{20,}={0,2}$`)                             // Base64
	jwtRegex       = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]*\.eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*$`) // JWT
	objectIdRegex  = regexp.MustCompile(`^[0-9a-fA-F]{24}$`)                                      // MongoDB ObjectId
	shortHashRegex = regexp.MustCompile(`^[0-9a-fA-F]{7,8}$`)                                     // Git short hash
	emailRegex     = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)       // Email
	ipv4Regex      = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)                   // IPv4
	phoneRegex     = regexp.MustCompile(`^1[3-9]\d{9}$`)                                          // 手机号
	idCardRegex    = regexp.MustCompile(`^\d{17}[\dXx]$`)                                         // 身份证
	fileHashRegex  = regexp.MustCompile(`^[a-fA-F0-9]{32}$|^[a-fA-F0-9]{40}$|^[a-fA-F0-9]{64}$`)  // MD5/SHA1/SHA256
	snowflakeRegex = regexp.MustCompile(`^\d{18,19}$`)                                            // Snowflake ID
)

func (ei *EngineInfo) InitNormalize() {
	ei.PendingNormalizeQueue = make(chan *PendingUrl, 100)
	ei.NormalizeCloseChan = make(chan int)
	ei.normalizeResultMu = sync.RWMutex{}
	ei.NormalizeationResultMap = make(map[string]int)
	ei.normalizeStaticMu = sync.RWMutex{}
	ei.NormalizeationStaticMap = make(map[string]int)
	ei.NormalizeCloseChanFlag = false
	go ei.normalizeWork()
}

func (ei *EngineInfo) pushPendingNormalizeQueue(pu *PendingUrl) {
	// 管道关闭了就不发送数据了
	if ei.NormalizeCloseChanFlag {
		return
	}
	defer func() {
		// 收尾阶段可能出现关闭管道与生产并发，避免因 send on closed channel 直接崩溃。
		_ = recover()
	}()
	ei.PendingNormalizeQueue <- pu
}

func (ei *EngineInfo) normalizeWork() {
	// 泛化管道 接收流量劫持的
	for {
		data, ok := <-ei.PendingNormalizeQueue
		if !ok {
			ei.NormalizeCloseChan <- 0
			return
		}
		// 获取后缀
		urlStr := data.URL
		if canonical, err := utils.CanonicalizeURL(urlStr, ""); err == nil {
			urlStr = canonical
		}
		// http://testphp.vulnweb.com/AJAX/styles.css#2378123687
		idx := strings.LastIndex(urlStr, "#")
		if idx != -1 {
			urlStr = urlStr[:idx]
		}
		if !filterStatic(urlStr) {
			value := normalizeation(urlStr, data.Method)

			// 使用互斥锁保护并发访问
			ei.normalizeResultMu.Lock()
			_, exists := ei.NormalizeationResultMap[value]
			if !exists {
				ei.NormalizeationResultMap[value] = 0
				// 检查缓存限制
				if len(ei.NormalizeationResultMap) > normalizeCacheLimit {
					log.Logger.Warnf("normalize cache limit reached, clearing oldest entries")
					// 清理一半的缓存，而不是全部清空，避免重复爬取
					ei.cleanupResultCache()
				}
			}
			ei.normalizeResultMu.Unlock()

			if !exists {
				ei.pushResult(data)
			}
		}
	}
}

// cleanupResultCache 清理一半的缓存
func (ei *EngineInfo) cleanupResultCache() {
	// 简单的策略：清理一半
	count := 0
	limit := len(ei.NormalizeationResultMap) / 2
	for k := range ei.NormalizeationResultMap {
		if count >= limit {
			break
		}
		delete(ei.NormalizeationResultMap, k)
		count++
	}
}

// isNumber 判断字符串是否是数字
func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// isFloat 判断字符串是否是浮点数
func isFloat(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func normalizeation(target, method string) string {
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	u.Fragment = ""
	pathStr := normalizePath(u.Path)
	queryStr := normalizeQuery(u)
	var builder strings.Builder
	builder.WriteString(strings.ToUpper(method))
	builder.WriteString("|")
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	builder.WriteString(scheme)
	builder.WriteString("://")
	builder.WriteString(strings.ToLower(u.Host))
	builder.WriteString(pathStr)
	if queryStr != "" {
		builder.WriteString("?")
		builder.WriteString(queryStr)
	}
	normalized := builder.String()
	if log.Logger != nil {
		log.Logger.Debugf("normalize url %s -> %s", target, normalized)
	}
	return utils.GetMD5(normalized)
}

func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	var builder strings.Builder
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			decoded = seg
		}
		builder.WriteString("/")
		builder.WriteString(classifyToken(decoded))
	}
	if builder.Len() == 0 {
		return "/"
	}
	return builder.String()
}

func normalizeQuery(u *url.URL) string {
	params := u.Query()
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)
	parts := make([]string, 0)
	for _, k := range keys {
		values := params[k]
		sort.Strings(values)
		if len(values) == 0 {
			parts = append(parts, k+"=")
			continue
		}
		for _, v := range values {
			parts = append(parts, k+"="+classifyToken(v))
		}
	}
	return strings.Join(parts, "&")
}

func classifyToken(token string) string {
	if token == "" {
		return token
	}
	clean := strings.TrimSpace(token)
	if clean == "" {
		return ""
	}

	// 基础类型检测
	if isNumber(clean) {
		return "{num}"
	}
	if isFloat(clean) {
		return "{float}"
	}

	// UUID
	if uuidRegex.MatchString(clean) {
		return "{uuid}"
	}

	// 日期
	if dateRegex.MatchString(clean) {
		return "{date}"
	}

	// 时间戳 (10-13位数字)
	if timestampRegex.MatchString(clean) {
		return "{timestamp}"
	}

	// Snowflake ID (18-19位数字)
	if snowflakeRegex.MatchString(clean) {
		return "{snowflake}"
	}

	// 版本号
	if versionRegex.MatchString(clean) {
		return "{version}"
	}

	// JWT Token
	if jwtRegex.MatchString(clean) {
		return "{jwt}"
	}

	// MongoDB ObjectId (24位十六进制)
	if objectIdRegex.MatchString(clean) {
		return "{objectid}"
	}

	// 文件哈希 (MD5/SHA1/SHA256)
	if fileHashRegex.MatchString(clean) {
		return "{hash}"
	}

	// 短哈希 (git commit)
	if shortHashRegex.MatchString(clean) {
		return "{shorthash}"
	}

	// Email
	if emailRegex.MatchString(clean) {
		return "{email}"
	}

	// 手机号
	if phoneRegex.MatchString(clean) {
		return "{phone}"
	}

	// 身份证
	if idCardRegex.MatchString(clean) {
		return "{idcard}"
	}

	// IPv4
	if ipv4Regex.MatchString(clean) {
		return "{ip}"
	}

	// Base64 (需要较长的字符串来确认)
	if len(clean) >= 20 && base64Regex.MatchString(clean) {
		return "{base64}"
	}

	// 长十六进制字符串
	if hexRegex.MatchString(clean) && len(clean) >= 8 {
		return "{hex}"
	}

	// 长随机字符串
	if !looksLikeSlug(clean) {
		if len(clean) > 40 {
			return "{token}"
		}
		return strings.ToLower(clean)
	}

	return strings.ToLower(clean)
}

func looksLikeSlug(s string) bool {
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (ei *EngineInfo) urlIsExists(target string) bool {
	// 用来给 静态url 判断的
	value := normalizeation(target, "GET")

	ei.normalizeStaticMu.Lock()
	defer ei.normalizeStaticMu.Unlock()

	if _, ok := ei.NormalizeationStaticMap[value]; !ok {
		ei.NormalizeationStaticMap[value] = 0
		return false
	}
	return true
}

func (ei *EngineInfo) CloseNormalizeQueue() {
	ei.NormalizeCloseChanFlag = true
	close(ei.PendingNormalizeQueue)
}

func (ei *EngineInfo) PendingNormalizeQueueEmpty() {
	<-ei.NormalizeCloseChan
}
