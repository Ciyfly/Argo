package engine

import (
	"sync"
	"testing"
	"time"
)

func TestEventSubscription(t *testing.T) {
	ei := &EngineInfo{}
	var wg sync.WaitGroup
	wg.Add(1)
	ei.SubscribeEvents(func(evt EngineEvent) {
		if evt.Type != "url_submit" {
			t.Fatalf("unexpected event type %s", evt.Type)
		}
		wg.Done()
	})
	ei.EmitEvent(EngineEvent{Type: "url_submit", Timestamp: time.Now()})
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event handler not invoked")
	}
}
