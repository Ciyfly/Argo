package engine

import (
	"context"
	"sync/atomic"
	"time"

	"argo/pkg/log"
)

// GlobalIdleSnapshot 用于在调试时观测“是否真的空闲”。
// 这里的“空闲”定义：调度器无待处理 URL + 无浏览器 Tab 在跑 + StandardEngine 无队列/无在飞 + 后台发现任务已结束。
type GlobalIdleSnapshot struct {
	Scheduler SchedulerSnapshot      `json:"scheduler"`
	Standard  StandardEngineSnapshot `json:"standard_engine"`
	BgTasks   int64                  `json:"bg_tasks"`
}

func (ei *EngineInfo) runBackground(name string, fn func()) {
	if fn == nil {
		return
	}
	atomic.AddInt64(&ei.bgTasks, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Logger.Warnf("background task panic: %s err=%v", name, r)
			}
		}()
		defer atomic.AddInt64(&ei.bgTasks, -1)
		fn()
	}()
}

func (ei *EngineInfo) globalIdleSnapshot() GlobalIdleSnapshot {
	var snap GlobalIdleSnapshot
	if ei == nil {
		return snap
	}
	if ei.Scheduler != nil {
		snap.Scheduler = ei.Scheduler.Snapshot()
	}
	if ei.DualEngine != nil {
		snap.Standard = ei.DualEngine.StandardSnapshot()
	}
	snap.BgTasks = atomic.LoadInt64(&ei.bgTasks)
	return snap
}

func (ei *EngineInfo) isGloballyIdle() bool {
	if ei == nil {
		return true
	}
	// 1) 后台发现任务
	if atomic.LoadInt64(&ei.bgTasks) > 0 {
		return false
	}

	// 2) Scheduler
	if ei.Scheduler != nil && !ei.Scheduler.IsIdle() {
		return false
	}

	// 3) DualEngine(StandardEngine)
	if ei.DualEngine != nil && ei.DualEngine.config != nil && ei.DualEngine.config.Enabled {
		if !ei.DualEngine.IsStandardIdle() {
			return false
		}
	}

	return true
}

// waitGlobalIdle 等待“稳定空闲”持续 stableFor 时间后再返回 true。
// 这样可以规避瞬时空闲(例如 ingestLoop 正在把 submitCh 转入 pq)导致的误判。
func (ei *EngineInfo) waitGlobalIdle(ctx context.Context, stableFor time.Duration) bool {
	if stableFor <= 0 {
		stableFor = 2 * time.Second
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var idleSince time.Time
	var logged bool

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if ei.isGloballyIdle() {
				if idleSince.IsZero() {
					idleSince = time.Now()
				}
				if !logged && time.Since(idleSince) >= 500*time.Millisecond {
					snap := ei.globalIdleSnapshot()
					log.Logger.Debugf("[idle candidate] scheduler=%+v standard=%+v bg=%d", snap.Scheduler, snap.Standard, snap.BgTasks)
					logged = true
				}
				if time.Since(idleSince) >= stableFor {
					return true
				}
				continue
			}
			idleSince = time.Time{}
			logged = false
		}
	}
}
