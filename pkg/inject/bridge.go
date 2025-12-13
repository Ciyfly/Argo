package inject

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"argo/pkg/conf"
	"argo/pkg/log"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

type autoBridgeEvent struct {
	Name      string
	Payload   map[string]interface{}
	Timestamp time.Time
}

type autoBridgeStats struct {
	EstimatedMs     int64
	Batches         int64
	Actions         int64
	LastQueue       int64
	TotalDurationMs int64
	BridgeEnabled   bool
}

type autoBridgeCommand struct {
	Type string   `json:"type"`
	Slow *float64 `json:"slow,omitempty"`
}

type autoBridge struct {
	page    *rod.Page
	enabled bool
	dispose func() error

	events chan autoBridgeEvent
	ctx    context.Context
	cancel context.CancelFunc

	stats autoBridgeStats
	mu    sync.Mutex

	extend func(time.Duration)
}

func newAutoBridge(page *rod.Page, extend func(time.Duration)) *autoBridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &autoBridge{
		page:   page,
		events: make(chan autoBridgeEvent, 64),
		ctx:    ctx,
		cancel: cancel,
		extend: extend,
	}

	dispose, err := page.Expose("__argoBridgeEmit", func(payload gson.JSON) (interface{}, error) {
		event := payload.Get("event").Str()
		raw := payload.Get("payload").Val()
		data, ok := raw.(map[string]interface{})
		if !ok || data == nil {
			data = map[string]interface{}{}
		}
		select {
		case b.events <- autoBridgeEvent{Name: event, Payload: data, Timestamp: time.Now()}:
		default:
			log.Logger.Warnf("auto bridge event queue full, drop event=%s", event)
		}
		return nil, nil
	})
	if err != nil {
		log.Logger.Debugf("auto bridge expose failed: %v", err)
		cancel()
		close(b.events)
		return b
	}

	b.enabled = true
	b.dispose = dispose
	go b.loop()
	return b
}

func (b *autoBridge) loop() {
	for {
		select {
		case evt, ok := <-b.events:
			if !ok {
				return
			}
			b.handleEvent(evt)
		case <-b.ctx.Done():
			return
		}
	}
}

func (b *autoBridge) handleEvent(evt autoBridgeEvent) {
	if evt.Name == "" {
		return
	}
	log.Logger.Debugf("[auto-event] %s payload=%v", evt.Name, evt.Payload)
	switch evt.Name {
	case "budget_proposal":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
			if v, ok := readNumber(evt.Payload["estimated_ms"]); ok {
				stats.EstimatedMs = v
			}
			if v, ok := readNumber(evt.Payload["queue"]); ok {
				stats.LastQueue = v
			}
		})
		b.extendFromPayload(evt.Payload, "estimated_ms")
		b.sendCommand(autoBridgeCommand{Type: "continue"})
	case "batch_done":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
			stats.Batches++
			if v, ok := readNumber(evt.Payload["total_actions"]); ok {
				stats.Actions = v
			} else if v, ok := readNumber(evt.Payload["actions"]); ok {
				stats.Actions += v
			}
			if v, ok := readNumber(evt.Payload["queue_left"]); ok {
				stats.LastQueue = v
			}
			if v, ok := readNumber(evt.Payload["duration_ms"]); ok {
				stats.TotalDurationMs += v
			}
		})
		b.extendFromPayload(evt.Payload, "extend_ms")
		b.sendCommand(autoBridgeCommand{Type: "continue"})
	case "final_result":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
			if v, ok := readNumber(evt.Payload["duration_ms"]); ok {
				stats.TotalDurationMs = v
			}
			if v, ok := readNumber(evt.Payload["total_actions"]); ok {
				stats.Actions = v
			}
			if v, ok := readNumber(evt.Payload["batches"]); ok {
				stats.Batches = v
			}
		})

	// 新增：框架就绪事件 - 表示JS正在等待框架渲染，需要延长超时
	case "framework_ready":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
		})
		// 框架初始化完成，给予额外的处理时间
		if waitMs, ok := readNumber(evt.Payload["wait_time_ms"]); ok && waitMs > 0 {
			// 框架等待时间较长，给予相应的延长
			b.extend(time.Duration(waitMs*2) * time.Millisecond)
		} else {
			b.extend(3 * time.Second)
		}

	// 新增：DOM重扫描事件 - 发现了新元素需要处理
	case "dom_rescan":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
			if v, ok := readNumber(evt.Payload["queue_total"]); ok {
				stats.LastQueue = v
			}
		})
		// 根据队列大小给予延长
		if queueTotal, ok := readNumber(evt.Payload["queue_total"]); ok && queueTotal > 0 {
			// 每个元素预估500ms处理时间
			extendMs := queueTotal * 500
			if extendMs > 30000 {
				extendMs = 30000 // 最多延长30秒
			}
			b.extend(time.Duration(extendMs) * time.Millisecond)
		}

	// 新增：队列初始化事件 - 初始扫描完成
	case "queue_initialized":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
			if v, ok := readNumber(evt.Payload["total"]); ok {
				stats.LastQueue = v
			}
		})
		// 根据初始队列大小延长
		if total, ok := readNumber(evt.Payload["total"]); ok && total > 0 {
			extendMs := total * 300
			if extendMs > 60000 {
				extendMs = 60000
			}
			b.extend(time.Duration(extendMs) * time.Millisecond)
		}

	// 新增：弹窗处理事件 - 正在处理弹窗
	case "popup_detected", "popup_content":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
		})
		// 弹窗处理需要额外时间
		b.extend(2 * time.Second)

	// 新增：Shadow DOM/iframe 扫描事件
	case "shadow_dom_scanned", "iframes_processed":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
		})
		if count, ok := readNumber(evt.Payload["elements_found"]); ok && count > 0 {
			extendMs := count * 200
			if extendMs > 10000 {
				extendMs = 10000
			}
			b.extend(time.Duration(extendMs) * time.Millisecond)
		} else if count, ok := readNumber(evt.Payload["count"]); ok && count > 0 {
			b.extend(time.Duration(count*1000) * time.Millisecond)
		}

	// 新增：自适应延迟统计 - 表示JS仍在活跃运行
	case "adaptive_delay_stats":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
		})
		// 有pending请求时给予更多时间
		if pending, ok := readNumber(evt.Payload["pending_requests"]); ok && pending > 0 {
			b.extend(time.Duration(pending*500+2000) * time.Millisecond)
		} else {
			b.extend(2 * time.Second)
		}

	// 新增：bridge_ready 事件 - JS桥接已准备好
	case "bridge_ready":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
		})
		// 初始化完成，给予基础时间
		b.extend(5 * time.Second)

	// 新增：心跳事件 - JS定期上报进度
	case "heartbeat":
		b.updateStats(func(stats *autoBridgeStats) {
			stats.BridgeEnabled = true
			if v, ok := readNumber(evt.Payload["actions"]); ok {
				stats.Actions = v
			}
			if v, ok := readNumber(evt.Payload["batches"]); ok {
				stats.Batches = v
			}
			if v, ok := readNumber(evt.Payload["queue_total"]); ok {
				stats.LastQueue = v
			}
		})
		// 根据心跳数据延长超时
		b.extendFromPayload(evt.Payload, "extend_ms")

	default:
		// best effort logging, no-op
	}
}

func (b *autoBridge) updateStats(fn func(*autoBridgeStats)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn(&b.stats)
}

func readNumber(val interface{}) (int64, bool) {
	switch v := val.(type) {
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case int:
		return int64(v), true
	case int64:
		return v, true
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func (b *autoBridge) sendCommand(cmd autoBridgeCommand) {
	if !b.enabled {
		return
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return
	}
	script := fmt.Sprintf(`(function(){ if (window.__argoBridgePushCommand) { window.__argoBridgePushCommand(%s); } })()`, string(data))
	_, err = (&proto.RuntimeEvaluate{
		Expression:    script,
		ReturnByValue: true,
	}).Call(b.page)
	if err != nil {
		log.Logger.Debugf("auto bridge send command err: %v", err)
	}
}

func (b *autoBridge) extendFromPayload(payload map[string]interface{}, key string) {
	if b.extend == nil {
		return
	}
	ms, ok := readNumber(payload[key])
	if !ok || ms <= 0 {
		return
	}
	d := time.Duration(ms) * time.Millisecond
	if d <= 0 {
		return
	}
	b.extend(d)
}

func (b *autoBridge) Enabled() bool {
	return b != nil && b.enabled
}

func (b *autoBridge) Close() {
	if b == nil {
		return
	}
	b.cancel()
	if b.dispose != nil {
		_ = b.dispose()
	}
	close(b.events)
}

func (b *autoBridge) Stats() autoBridgeStats {
	if b == nil {
		return autoBridgeStats{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stats
}

func (b *autoBridge) SuggestTimeout() time.Duration {
	tabTimeout := time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second
	if tabTimeout <= 0 {
		tabTimeout = 180 * time.Second
	}

	// 计算基础预估时间
	estimated := EstimateAutoDuration()
	if estimated <= 0 {
		estimated = 15 * time.Second
	}

	// 考虑框架初始化时间（Vue/React等需要额外时间）
	frameworkBuffer := 5 * time.Second

	// 考虑网络延迟和DOM变化
	networkBuffer := 3 * time.Second

	// 总预估 = 基础预估 + 框架缓冲 + 网络缓冲
	totalEstimated := estimated + frameworkBuffer + networkBuffer

	// 软超时配置
	softTimeout := time.Duration(conf.GlobalConfig.BrowserConf.TabSoftTimeout) * time.Second
	if softTimeout <= 0 {
		softTimeout = 60 * time.Second
	}

	// 使用软超时和预估时间中较大的值作为初始超时
	// 这样即使预估不准确，也有更多时间通过心跳延长
	suggestedTimeout := totalEstimated
	if softTimeout > suggestedTimeout {
		suggestedTimeout = softTimeout
	}

	// 但不能超过硬超时
	if suggestedTimeout+5*time.Second > tabTimeout {
		suggestedTimeout = tabTimeout - time.Second
	}

	// 最小保证
	if suggestedTimeout < 10*time.Second {
		suggestedTimeout = 10 * time.Second
	}

	return suggestedTimeout
}
