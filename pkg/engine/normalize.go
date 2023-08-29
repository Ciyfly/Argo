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
)

// 泛化去重

var PendingNormalizeQueue chan *PendingUrl
var NormalizeCloseChan chan int
var NormalizeCloseChanFlag bool
var NormalizeationResultMap map[string]int
var NormalizeationPendUrlMap map[string]int

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

var mutex sync.Mutex

func InitNormalize() {
	PendingNormalizeQueue = make(chan *PendingUrl, 10000)
	NormalizeCloseChan = make(chan int)
	NormalizeationResultMap = make(map[string]int)
	NormalizeationPendUrlMap = make(map[string]int)
	NormalizeCloseChanFlag = false
	go normalizeWork()
}

func pushpendingNormalizeQueue(pu *PendingUrl) {
	// 管道关闭了就不发送数据了
	if NormalizeCloseChanFlag {
		return
	}
	PendingNormalizeQueue <- pu
}

func normalizeWork() {
	// 泛化管道 接收流量劫持的
	for {
		data, ok := <-PendingNormalizeQueue
		if !ok {
			NormalizeCloseChan <- 0
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
			if _, ok := NormalizeationResultMap[value]; !ok {
				NormalizeationResultMap[value] = 0
				pushResult(data)
			}
		}
	}

}

// isNumber 判断字符串是否是数字
func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func normalizeationPath(pathStr string) string {
	normalizedUrl := pathStr
	var numRe = regexp.MustCompile(`\d+`)
	normalizedUrl = numRe.ReplaceAllStringFunc(normalizedUrl, func(s string) string {
		return "number"
	})
	if len(normalizedUrl) > 0 && normalizedUrl[len(normalizedUrl)-1] != '/' {
		normalizedUrl += "/"
	}
	return normalizedUrl
}

func normalizeation(target, method string) string {
	u, _ := url.Parse(target)
	// 参数泛化
	params := u.Query()
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var paramsStr string
	for _, k := range keys {
		values := params[k]
		for _, v := range values {
			if isNumber(v) {
				paramsStr += k + "=" + "@"
			} else {
				paramsStr += k + "=" + "$"
			}
		}
	}
	// path 泛化
	normalizeStr := strings.ToLower(u.Host)
	if u.Path != "" {
		norPath := normalizeationPath(u.Path)
		normalizeStr += norPath
	}
	if paramsStr != "" {
		normalizeStr += paramsStr
	}
	// 对于 page/1 page/2 这种url进行处理 认为只有一个url
	pathList := strings.Split(u.Path, "/")
	if isNumber(pathList[len(pathList)-1]) {
		normalizeStr = "|" + u.Scheme + "://" + u.Host + strings.Join(pathList[:len(pathList)-1], "/") + "/@"
	} else {
		normalizeStr = method + "|" + normalizeStr
	}
	// 使用 strings.Builder 来构建字符串
	var sb strings.Builder
	sb.WriteString("normalizeStr url ")
	sb.WriteString(u.String())
	sb.WriteString(" -> ")
	sb.WriteString(normalizeStr)
	log.Logger.Debugf(sb.String())

	return utils.GetMD5(normalizeStr)
}

func urlIsExists(target string) bool {
	// 用来给 静态url 判断的
	value := normalizeation(target, "GET")
	mutex.Lock()
	if _, ok := NormalizeationPendUrlMap[value]; !ok {
		NormalizeationPendUrlMap[value] = 0
		mutex.Unlock()
		return false
	}
	mutex.Unlock()
	return true
}

func CloseNormalizeQueue() {
	NormalizeCloseChanFlag = true
	close(PendingNormalizeQueue)
}

func PendingNormalizeQueueEmpty() {
	<-NormalizeCloseChan
}
