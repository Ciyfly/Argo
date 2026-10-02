package engine

import (
	"sync/atomic"
	"time"
)

// tabProgressTracker 记录单页最近一次「有进展」的时刻。
//
// 固定 tab 超时的两头不讨好：设短了元素多的页面交互被拦腰砍断
// （购物车/多级菜单后半段全丢），设长了挂起页面把整个任务拖死。
// 改为进度驱动：页面还在出新链接就让它活着，静默超过 idle 阈值才杀
// （katana 的 heuristic 加载策略同理——等静默信号，不给页面固定寿命）。
type tabProgressTracker struct {
	lastProgressUnixNano atomic.Int64
}

// Mark 记录一次进展（新链接入队、交互收集到新链接等）。
func (t *tabProgressTracker) Mark() {
	t.lastProgressUnixNano.Store(time.Now().UnixNano())
}

// IdleFor 距最近一次进展过了多久。
func (t *tabProgressTracker) IdleFor() time.Duration {
	last := t.lastProgressUnixNano.Load()
	if last == 0 {
		return 0
	}
	return time.Since(time.Unix(0, last))
}
