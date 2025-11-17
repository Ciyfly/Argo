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
	estimated := EstimateAutoDuration()
	if estimated <= 0 {
		estimated = 15 * time.Second
	}
	if estimated+5*time.Second > tabTimeout {
		estimated = tabTimeout - time.Second
	}
	if estimated < 5*time.Second {
		estimated = 5 * time.Second
	}
	return estimated
}
