package engine

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func initTestResultEnv(t *testing.T) {
	t.Helper()
	log.Init(false, true)
	conf.GlobalConfig = &conf.Conf{}
	conf.GlobalConfig.Quiet = false
	ctx, cancel := context.WithCancel(context.Background())
	InitResultHandler(ctx)
	t.Cleanup(cancel)
}

// 回归测试：结果列表被后台协程持续写入时，快照必须稳定且不丢数据。
// 修复前写文件函数直接遍历全局 ResultList，与后台 append 并发会产生数据竞争 / 丢结果。
func TestSnapshotResultIsStable(t *testing.T) {
	initTestResultEnv(t)

	const total = 300
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			pushResult(&PendingUrl{URL: fmt.Sprintf("http://example.com/%d", i), Method: "GET"})
		}
	}()

	// 与写入并发地反复取快照，快照本身不能 panic，也不能出现「读到的条数倒退」
	prev := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap := SnapshotResult()
		if len(snap) < prev {
			t.Fatalf("快照条数倒退：上一次 %d，这次 %d", prev, len(snap))
		}
		prev = len(snap)
		if prev == total {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	wg.Wait()
	FlushResults(3 * time.Second)

	if got := resultCount(); got != total {
		t.Errorf("结果数量不对：期望 %d，实际 %d（并发下有丢失）", total, got)
	}
}

// 回归测试：FlushResults 必须等队列排空，否则浏览器超时被强关时会丢掉队列里的结果。
func TestFlushResultsWaitsForQueue(t *testing.T) {
	initTestResultEnv(t)

	const total = 50
	for i := 0; i < total; i++ {
		pushResult(&PendingUrl{URL: fmt.Sprintf("http://example.com/flush/%d", i), Method: "GET"})
	}
	FlushResults(3 * time.Second)

	if got := resultCount(); got != total {
		t.Errorf("FlushResults 后结果数量不对：期望 %d，实际 %d", total, got)
	}
}

// 回归测试：多目标时必须清空上一轮结果，否则前一个目标的 URL 会串进下一个目标的输出文件。
func TestResetResultBetweenTargets(t *testing.T) {
	initTestResultEnv(t)

	pushResult(&PendingUrl{URL: "http://first-target.com/", Method: "GET"})
	FlushResults(2 * time.Second)
	if resultCount() == 0 {
		t.Fatalf("第一个目标的结果没进去，测试前提不成立")
	}

	ResetResult()
	if got := resultCount(); got != 0 {
		t.Errorf("ResetResult 之后结果应清空，实际还有 %d 条", got)
	}
	if got := len(SnapshotResult()); got != 0 {
		t.Errorf("ResetResult 之后快照应为空，实际 %d 条", got)
	}

	pushResult(&PendingUrl{URL: "http://second-target.com/", Method: "GET"})
	FlushResults(2 * time.Second)
	snap := SnapshotResult()
	if len(snap) != 1 || snap[0].URL != "http://second-target.com/" {
		t.Errorf("第二个目标的结果不应包含上一个目标的 URL，实际: %+v", snap)
	}
}
