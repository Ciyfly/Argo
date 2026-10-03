package engine

import (
	"argo/pkg/log"
	"argo/pkg/utils"
	"context"
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

// 按「泛化模式」计数，用于防止同一模式无限翻页。
//
// 历史做法是把参数值直接泛化成占位符当作去重键，
// 结果 ?id=1 / ?id=2 / ?id=99 全被当成同一个 URL，
// 只留第一条，其余全丢（靶场实测 query 类 1/4）。
//
// 现在改为：去重键保留具体值（不同页面就是不同页面），
// 只对同一模式限制总条数，兼顾「不丢页面」与「不无限翻」。
var (
	normalizePatternCount map[string]int
)

// maxURLsPerPattern 同一泛化模式最多允许出现的不同 URL 数。
// 超过后视为翻页噪声丢弃，避免 ?id=1..100000 把队列撑爆。
const maxURLsPerPattern = 50

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

func InitNormalize(ctx context.Context) {
	PendingNormalizeQueue = make(chan *PendingUrl, 10000)
	NormalizeCloseChan = make(chan int)
	NormalizeationResultMap = make(map[string]int)
	NormalizeationPendUrlMap = make(map[string]int)
	normalizePatternCount = make(map[string]int)
	NormalizeCloseChanFlag = false
	go normalizeWork(ctx)
}

func pushpendingNormalizeQueue(pu *PendingUrl) {
	// 管道关闭了就不发送数据了
	if NormalizeCloseChanFlag {
		return
	}
	PendingNormalizeQueue <- pu
}

func normalizeWork(ctx context.Context) {
	// 泛化管道 接收流量劫持的
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-PendingNormalizeQueue:
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
				exactKey := normalizeation(urlStr, data.Method)
				patternKey := normalizeationPattern(urlStr, data.Method)
				if _, ok := NormalizeationResultMap[exactKey]; !ok {
					// 同一模式超过上限则丢弃（防无限翻页），否则按精确 URL 去重
					if normalizePatternCount[patternKey] >= maxURLsPerPattern {
						log.Logger.Debugf("normalize pattern limit reached: %s", urlStr)
						continue
					}
					NormalizeationResultMap[exactKey] = 0
					normalizePatternCount[patternKey]++
					pushResult(data)
				}
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

// normalizeation 生成「精确去重键」：保留 URL 的具体内容。
//
// 历史实现把参数值和 path 里的数字都替换成占位符，
// 导致 ?id=1 / ?id=2 / ?id=99 被当成同一个 URL，
// 分页路径也相互覆盖（靶场实测 query 类 1/4、html 类 10/13）。
//
// 不同页面就是不同页面，这里不再合并；
// 防止无限翻页交给 normalizeationPattern + maxURLsPerPattern。
func normalizeation(target, method string) string {
	u, _ := url.Parse(target)
	key := method + "|" + strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.Path
	// query 按 key 排序，保证 ?a=1&b=2 与 ?b=2&a=1 视为同一个
	params := u.Query()
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		values := append([]string(nil), params[k]...)
		sort.Strings(values)
		for _, v := range values {
			key += "&" + k + "=" + v
		}
	}
	log.Logger.Debugf("normalizeStr url %s -> %s", u.String(), key)
	return utils.GetMD5(key)
}

// normalizeationPattern 生成「泛化模式键」：把易变部分抹平。
//
// 用于统计同一类 URL 出现了多少条，超过 maxURLsPerPattern 就不再收录，
// 避免 ?id=1..100000 或 /page/1..N 把队列撑爆。
func normalizeationPattern(target, method string) string {
	u, _ := url.Parse(target)
	pattern := strings.ToLower(u.Host)
	if u.Path != "" {
		pattern += normalizeationPath(u.Path)
	}
	// 参数只保留 key 和「值的类型」
	params := u.Query()
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range params[k] {
			if isNumber(v) {
				pattern += k + "=@"
			} else {
				pattern += k + "=$"
			}
		}
	}
	// 形如 /page/1 /page/2 的翻页路径归为同一个模式
	pathList := strings.Split(u.Path, "/")
	if len(pathList) > 0 && isNumber(pathList[len(pathList)-1]) {
		pattern = "|" + u.Scheme + "://" + u.Host + strings.Join(pathList[:len(pathList)-1], "/") + "/@"
	} else {
		pattern = method + "|" + pattern
	}
	return utils.GetMD5(pattern)
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

// ---- 断点续爬支持：已访问键导出/导入 + UrlsQueue 积压的影子集合 ----
//
// UrlsQueue 是 chan 无法快照；PushUrlQueue 时登记、PendUrlWork 消费后移除，
// 影子集合即「已发现未消费」的断点状态。

var pendingShadowMu sync.Mutex
var pendingShadow map[string]*UrlInfo

// RegisterPendingShadow 登记 URL 进入待处理影子集合。
func RegisterPendingShadow(uif *UrlInfo) {
	pendingShadowMu.Lock()
	if pendingShadow == nil {
		pendingShadow = make(map[string]*UrlInfo)
	}
	pendingShadow[uif.Url] = uif
	pendingShadowMu.Unlock()
}

// ConsumePendingShadow 从影子集合移除（已消费或已丢弃）。
func ConsumePendingShadow(rawURL string) {
	pendingShadowMu.Lock()
	delete(pendingShadow, rawURL)
	pendingShadowMu.Unlock()
}

// SnapshotPendingShadow 返回待处理 URL 列表副本。
func SnapshotPendingShadow() []*UrlInfo {
	pendingShadowMu.Lock()
	defer pendingShadowMu.Unlock()
	out := make([]*UrlInfo, 0, len(pendingShadow))
	for _, uif := range pendingShadow {
		out = append(out, uif)
	}
	return out
}

// VisitedKeysSnapshot 导出已访问 URL 的去重键（断点续爬用）。
func VisitedKeysSnapshot() []string {
	mutex.Lock()
	defer mutex.Unlock()
	keys := make([]string, 0, len(NormalizeationPendUrlMap))
	for k := range NormalizeationPendUrlMap {
		keys = append(keys, k)
	}
	return keys
}

// ImportVisitedKeys 批量注入已访问键（--resume 恢复时调用）。
func ImportVisitedKeys(keys []string) {
	mutex.Lock()
	for _, k := range keys {
		NormalizeationPendUrlMap[k] = 0
	}
	mutex.Unlock()
}

func CloseNormalizeQueue() {
	NormalizeCloseChanFlag = true
	close(PendingNormalizeQueue)
}

func PendingNormalizeQueueEmpty() {
	<-NormalizeCloseChan
}
