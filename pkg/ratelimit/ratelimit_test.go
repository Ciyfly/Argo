package ratelimit

import (
	"context"
	"testing"
	"time"
)

// 限速关闭（速率 0）时必须完全直通，不能引入任何等待——这是默认路径。
func TestWaitNavigationUnlimited(t *testing.T) {
	Init(0, 0)
	start := time.Now()
	WaitNavigation(context.Background(), "http://a.com/page")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("不限速时不应有等待，实际等待 %s", elapsed)
	}
}

// 全局限速 50/s 时，连续 10 次导航应耗时约 (10-burst) 个间隔以上。
// burst=50 会先放行 50 个，所以这里用「100 次导航、限速 50/s」验证总耗时下限。
func TestWaitNavigationGlobalRate(t *testing.T) {
	Init(50, 0)
	start := time.Now()
	for i := 0; i < 100; i++ {
		WaitNavigation(context.Background(), "http://a.com/page")
	}
	elapsed := time.Since(start)
	// 100 次 - burst 50 = 50 次需要等待令牌，至少 50/50s = 1s
	if elapsed < 900*time.Millisecond {
		t.Errorf("限速 50/s 下 100 次导航耗时 %s，未达到预期下限 ~1s", elapsed)
	}
	Init(0, 0)
}

// 单 host 限速只约束同一 host，不同 host 互不影响。
func TestWaitNavigationHostIsolation(t *testing.T) {
	Init(0, 5) // host 5/s，burst 5
	ctx := context.Background()
	// a.com 消耗完 burst
	for i := 0; i < 5; i++ {
		WaitNavigation(ctx, "http://a.com/page")
	}
	// b.com 的 bucket 独立，5 次应当瞬间完成
	start := time.Now()
	for i := 0; i < 5; i++ {
		WaitNavigation(ctx, "http://b.com/page")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("不同 host 应有独立令牌桶，实际等待 %s", elapsed)
	}
	Init(0, 0)
}

// ctx 取消后 WaitNavigation 必须立即返回，否则浏览器超时退出会被限速器卡住。
func TestWaitNavigationContextCancel(t *testing.T) {
	Init(1, 0) // 1/s，burst 1
	ctx, cancel := context.WithCancel(context.Background())
	WaitNavigation(ctx, "http://a.com/1") // 消耗 burst
	cancel()
	start := time.Now()
	WaitNavigation(ctx, "http://a.com/2") // ctx 已取消
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("ctx 取消后不应阻塞，实际等待 %s", elapsed)
	}
	Init(0, 0)
}
