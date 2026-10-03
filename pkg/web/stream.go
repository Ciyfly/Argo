package web

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// streamHub：单页轮询截图 + 多客户端广播。
//
// 为什么不是 CDP screencast：本环境 chromium（snap 113，新旧 headless 均实测）
// 不产 screencastFrame 事件；退而求其次用低参数 JPEG 截图轮询——
// 实测 q60 截图 ~38-60ms/帧，配 75ms 节拍 ≈ 12fps，比初版 140ms/7fps 接近翻倍。
// 截图走 CDP 不进 DOM/网络，对爬取零干扰。
type streamHub struct {
	console *Console

	mu   sync.Mutex
	page *rod.Page
	gen  int64 // 页面代数：切换页面时递增，旧轮询协程自杀

	subs map[chan []byte]struct{}
}

// streamInterval 目标帧间隔（~12fps）
const streamInterval = 75 * time.Millisecond

func newStreamHub(c *Console) *streamHub {
	return &streamHub{console: c, subs: make(map[chan []byte]struct{})}
}

// subscribe 订阅帧流，返回取消函数。
func (h *streamHub) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 4)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// ensurePage 把轮询目标切到指定页（同页幂等）。
func (h *streamHub) ensurePage(page *rod.Page) {
	h.mu.Lock()
	if h.page == page && page != nil {
		h.mu.Unlock()
		return
	}
	h.page = page
	gen := atomic.AddInt64(&h.gen, 1)
	h.mu.Unlock()

	if page == nil {
		return
	}
	go h.pollLoop(page, gen)
}

// pollLoop 轮询截图并广播；页面切换（代数变化）或截图失败时退出。
func (h *streamHub) pollLoop(page *rod.Page, gen int64) {
	quality := 60
	req := proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatJpeg, Quality: &quality}
	for {
		if atomic.LoadInt64(&h.gen) != gen {
			return
		}
		start := time.Now()
		img, err := page.Screenshot(false, &req)
		if err != nil || len(img) == 0 {
			// 页面已关/导航：释放粘性引用，让跟随逻辑换下一页
			h.console.dropPage(page)
			return
		}
		h.broadcast(img)

		// 按捕获耗时自适应节拍：捕获慢就少睡，帧率稳定在目标值附近
		if d := streamInterval - time.Since(start); d > 0 {
			time.Sleep(d)
		}
	}
}

// broadcast 非阻塞发给所有订阅者（消费不过来丢帧，绝不拖慢截图循环）。
func (h *streamHub) broadcast(img []byte) {
	h.mu.Lock()
	subs := make([]chan []byte, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	for _, ch := range subs {
		select {
		case ch <- img:
		default:
		}
	}
}
