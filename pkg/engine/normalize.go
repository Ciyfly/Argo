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
	"unicode"
)

// 泛化去重

type PendingUrl struct {
	URL             string
	Method          string
	Host            string
	Headers         http.Header
	Data            string
	Status          int
	ResponseHeaders http.Header
	ResponseBody    string
	RequestStr      string
}

const normalizeCacheLimit = 500000

var (
	uuidRegex = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hexRegex  = regexp.MustCompile(`(?i)^[0-9a-f]+$`)
	dateRegex = regexp.MustCompile(`^\d{4}-\d{1,2}-\d{1,2}$`)
)

func (ei *EngineInfo) InitNormalize() {
	ei.PendingNormalizeQueue = make(chan *PendingUrl, 100)
	ei.NormalizeCloseChan = make(chan int)
	ei.NormalizeationResultMap = make(map[string]int)
	ei.NormalizeationStaticMap = make(map[string]int)
	ei.NormalizeCloseChanFlag = false
	go ei.normalizeWork()
}

func (ei *EngineInfo) pushPendingNormalizeQueue(pu *PendingUrl) {
	// 管道关闭了就不发送数据了
	if ei.NormalizeCloseChanFlag {
		return
	}
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
		// http://testphp.vulnweb.com/AJAX/styles.css#2378123687
		idx := strings.LastIndex(urlStr, "#")
		if idx != -1 {
			urlStr = urlStr[:idx]
		}
		if !filterStatic(urlStr) {
			value := normalizeation(urlStr, data.Method)
			if _, ok := ei.NormalizeationResultMap[value]; !ok {
				ei.NormalizeationResultMap[value] = 0
				if len(ei.NormalizeationResultMap) > normalizeCacheLimit {
					log.Logger.Warnf("normalize cache limit reached, clearing")
					ei.NormalizeationResultMap = make(map[string]int)
				}
				ei.pushResult(data)
			}
		}
	}

}

// isNumber 判断字符串是否是数字
func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
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
