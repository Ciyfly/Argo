package ratelimit

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// 导航限速：全局每秒页面数 + 单 host 每秒页面数。
//
// 只挂在「我们主动发起」的导航层（TabWork 起 tab 前）：
// hijack 回调里不能 Wait——那会阻塞浏览器的流量管道，页面渲染本身就依赖这些请求。
//
// 速率配置为 0 时完全直通（保持历史行为），默认即不限速。

var (
	globalLimiter *rate.Limiter
	hostLimiter   *HostLimiter
)

// Init 初始化限速器。两个速率都是「每秒次数」，0 表示不限。
// 在每个目标的 engine 初始化时调用一次即可。
func Init(globalPerSecond int, hostPerSecond int) {
	globalLimiter = nil
	hostLimiter = nil
	if globalPerSecond > 0 {
		globalLimiter = rate.NewLimiter(rate.Limit(globalPerSecond), globalPerSecond)
	}
	if hostPerSecond > 0 {
		hostLimiter = &HostLimiter{
			perSecond: hostPerSecond,
			buckets:   make(map[string]*rate.Limiter),
		}
	}
}

// WaitNavigation 在访问一个页面前调用：依次等待全局与单 host 令牌。
// ctx 取消（浏览器超时/任务结束）时立即返回，不拖慢退出。
func WaitNavigation(ctx context.Context, rawURL string) {
	if globalLimiter != nil {
		if err := globalLimiter.Wait(ctx); err != nil {
			return
		}
	}
	if hostLimiter != nil {
		hostLimiter.wait(ctx, rawURL)
	}
}

// HostLimiter 按请求 URL 的 host 维护独立的令牌桶。
type HostLimiter struct {
	mu        sync.Mutex
	perSecond int
	buckets   map[string]*rate.Limiter
}

func (h *HostLimiter) wait(ctx context.Context, rawURL string) {
	host := ""
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	// URL 解析不出 host 的（如 about:blank）不值得限速，直接放行
	if host == "" {
		return
	}
	h.mu.Lock()
	bucket, ok := h.buckets[host]
	if !ok {
		bucket = rate.NewLimiter(rate.Limit(h.perSecond), h.perSecond)
		h.buckets[host] = bucket
	}
	h.mu.Unlock()
	_ = bucket.Wait(ctx)
}

// RetryBackoff 返回第 attempt 次（从 1 开始）重试前的等待时长，线性退避。
// 提出来是为了让 hijack 与静态请求共用同一退避策略。
func RetryBackoff(attempt int) time.Duration {
	return time.Duration(500*attempt) * time.Millisecond
}
