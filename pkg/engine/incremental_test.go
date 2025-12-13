package engine

import (
	"argo/pkg/log"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func init() {
	// 初始化日志以避免测试中的 nil pointer
	log.Init(false, true) // quiet mode
}

func TestIncrementalConfig_Default(t *testing.T) {
	config := DefaultIncrementalConfig()

	if config.Enabled {
		t.Error("Expected Enabled to be false by default")
	}
	if config.StateFile != ".argo_crawl_state.json" {
		t.Errorf("Expected StateFile='.argo_crawl_state.json', got '%s'", config.StateFile)
	}
	if config.MaxAge != 24*time.Hour {
		t.Errorf("Expected MaxAge=24h, got %v", config.MaxAge)
	}
	if config.AutoSaveInterval != 5*time.Minute {
		t.Errorf("Expected AutoSaveInterval=5m, got %v", config.AutoSaveInterval)
	}
	if !config.CompressState {
		t.Error("Expected CompressState to be true by default")
	}
	if config.MaxURLsInState != 1000000 {
		t.Errorf("Expected MaxURLsInState=1000000, got %d", config.MaxURLsInState)
	}
	if !config.ResumeFromPending {
		t.Error("Expected ResumeFromPending to be true by default")
	}
}

func TestIncrementalCrawler_Basic(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_crawl_state.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0, // 禁用自动保存
		CompressState:    false,
		MaxURLsInState:   1000,
	}

	crawler := NewIncrementalCrawler(nil, config)
	if crawler == nil {
		t.Fatal("NewIncrementalCrawler returned nil")
	}

	// 启动
	err := crawler.Start("https://example.com")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 清理测试文件
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	// 验证初始状态
	stats := crawler.GetStatistics()
	if stats == nil {
		t.Fatal("GetStatistics returned nil")
	}
	if stats.TotalCrawled != 0 {
		t.Errorf("Expected TotalCrawled=0, got %d", stats.TotalCrawled)
	}
}

func TestIncrementalCrawler_ShouldCrawl(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_should_crawl.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	url := "https://example.com/page1"
	hash := "hash1"

	// 第一次应该爬取
	shouldCrawl, reason := crawler.ShouldCrawl(url, hash)
	if !shouldCrawl {
		t.Errorf("Expected shouldCrawl=true for new URL, got false, reason: %s", reason)
	}

	// 标记为已爬取
	crawler.MarkCrawled(url, hash, 200, 1, "", 1024)

	// 第二次不应该爬取
	shouldCrawl, reason = crawler.ShouldCrawl(url, hash)
	if shouldCrawl {
		t.Errorf("Expected shouldCrawl=false for crawled URL, got true")
	}
	if reason == "" {
		t.Error("Expected non-empty reason for skipped URL")
	}
}

func TestIncrementalCrawler_MarkCrawled(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_mark_crawled.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	// 标记多个URL
	for i := 0; i < 10; i++ {
		crawler.MarkCrawled(
			"https://example.com/page",
			"hash"+string(rune('0'+i)),
			200, 1, "", 1024,
		)
	}

	// 验证统计
	stats := crawler.GetStatistics()
	if stats.TotalCrawled != 10 {
		t.Errorf("Expected TotalCrawled=10, got %d", stats.TotalCrawled)
	}

	count := crawler.GetCrawledCount()
	if count != 10 {
		t.Errorf("Expected GetCrawledCount=10, got %d", count)
	}
}

func TestIncrementalCrawler_MarkFailed(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_mark_failed.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	url := "https://example.com/failed"
	hash := "failhash"

	// 标记失败
	crawler.MarkFailed(url, hash, "connection timeout")

	// 再次失败
	crawler.MarkFailed(url, hash, "connection refused")

	// 第三次失败
	crawler.MarkFailed(url, hash, "503 error")

	// 第四次应该跳过（失败次数>=3）
	shouldCrawl, reason := crawler.ShouldCrawl(url, hash)
	if shouldCrawl {
		t.Error("Expected shouldCrawl=false for URL that failed 3+ times")
	}
	if reason == "" {
		t.Error("Expected non-empty reason for failed URL")
	}

	stats := crawler.GetStatistics()
	if stats.TotalFailed != 1 {
		t.Errorf("Expected TotalFailed=1, got %d", stats.TotalFailed)
	}
}

func TestIncrementalCrawler_PendingURLs(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_pending.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	// 添加待爬取URL
	crawler.AddPendingURL("https://example.com/page1", "hash1", 1, "", 1)
	crawler.AddPendingURL("https://example.com/page2", "hash2", 1, "", 2)
	crawler.AddPendingURL("https://example.com/page3", "hash3", 1, "", 3)

	if !crawler.HasPendingURLs() {
		t.Error("Expected HasPendingURLs=true")
	}

	// 获取待爬取列表（应该按优先级排序）
	pending := crawler.GetPendingURLs()
	if len(pending) != 3 {
		t.Errorf("Expected 3 pending URLs, got %d", len(pending))
	}

	// 验证优先级排序
	if pending[0].Priority != 3 {
		t.Errorf("Expected first pending URL to have priority=3, got %d", pending[0].Priority)
	}

	// 标记为已爬取后应从待爬取列表移除
	crawler.MarkCrawled("https://example.com/page3", "hash3", 200, 1, "", 0)

	pending = crawler.GetPendingURLs()
	if len(pending) != 2 {
		t.Errorf("Expected 2 pending URLs after marking one crawled, got %d", len(pending))
	}
}

func TestIncrementalCrawler_SaveLoad(t *testing.T) {
	stateFile := ".test_save_load.json"
	defer os.Remove(stateFile)

	// 创建并填充数据
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        stateFile,
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler1 := NewIncrementalCrawler(nil, config)
	crawler1.Start("https://example.com")

	crawler1.MarkCrawled("https://example.com/page1", "hash1", 200, 1, "", 1024)
	crawler1.MarkCrawled("https://example.com/page2", "hash2", 200, 2, "", 2048)
	crawler1.AddPendingURL("https://example.com/page3", "hash3", 1, "", 1)

	// 保存并停止
	err := crawler1.SaveState()
	if err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}
	crawler1.Stop()

	// 创建新实例并加载
	crawler2 := NewIncrementalCrawler(nil, config)
	err = crawler2.Start("https://example.com")
	if err != nil {
		t.Fatalf("Second crawler Start failed: %v", err)
	}
	defer crawler2.Stop()

	// 验证数据已恢复
	stats := crawler2.GetStatistics()
	if stats.TotalCrawled != 2 {
		t.Errorf("Expected TotalCrawled=2 after reload, got %d", stats.TotalCrawled)
	}

	if !crawler2.IsCrawled("hash1") {
		t.Error("Expected hash1 to be crawled after reload")
	}

	if crawler2.HasPendingURLs() {
		pending := crawler2.GetPendingURLs()
		if len(pending) != 1 {
			t.Errorf("Expected 1 pending URL after reload, got %d", len(pending))
		}
	}
}

func TestIncrementalCrawler_Checkpoint(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_checkpoint.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	// 创建检查点
	crawler.MarkCrawled("url1", "hash1", 200, 1, "", 0)
	cpID := crawler.CreateCheckpoint("test checkpoint")

	if cpID == "" {
		t.Error("Expected non-empty checkpoint ID")
	}

	// 验证检查点已创建
	if len(crawler.state.Checkpoints) != 1 {
		t.Errorf("Expected 1 checkpoint, got %d", len(crawler.state.Checkpoints))
	}

	if crawler.state.Checkpoints[0].Description != "test checkpoint" {
		t.Errorf("Unexpected checkpoint description: %s", crawler.state.Checkpoints[0].Description)
	}
}

func TestIncrementalCrawler_Clear(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_clear.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	// 添加数据
	crawler.MarkCrawled("url1", "hash1", 200, 1, "", 0)
	crawler.AddPendingURL("url2", "hash2", 1, "", 1)

	// 清空
	crawler.Clear()

	stats := crawler.GetStatistics()
	if stats.TotalCrawled != 0 {
		t.Errorf("Expected TotalCrawled=0 after Clear, got %d", stats.TotalCrawled)
	}

	if crawler.HasPendingURLs() {
		t.Error("Expected no pending URLs after Clear")
	}
}

func TestIncrementalCrawler_ExportImport(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_export.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	// 添加数据
	crawler.MarkCrawled("url1", "hash1", 200, 1, "", 1024)
	crawler.MarkCrawled("url2", "hash2", 200, 2, "", 2048)

	// 导出
	data, err := crawler.ExportState()
	if err != nil {
		t.Fatalf("ExportState failed: %v", err)
	}

	// 验证是有效的JSON
	var state CrawlState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Exported data is not valid JSON: %v", err)
	}

	// 清空并重新导入
	crawler.Clear()

	err = crawler.ImportState(data)
	if err != nil {
		t.Fatalf("ImportState failed: %v", err)
	}

	// 验证数据已恢复
	if crawler.GetCrawledCount() != 2 {
		t.Errorf("Expected 2 crawled URLs after import, got %d", crawler.GetCrawledCount())
	}
}

func TestIncrementalCrawler_Disabled(t *testing.T) {
	config := &IncrementalConfig{
		Enabled: false,
	}

	crawler := NewIncrementalCrawler(nil, config)

	// 禁用时所有操作应该安全返回
	err := crawler.Start("https://example.com")
	if err != nil {
		t.Errorf("Start should succeed when disabled: %v", err)
	}

	shouldCrawl, _ := crawler.ShouldCrawl("url", "hash")
	if !shouldCrawl {
		t.Error("ShouldCrawl should return true when disabled")
	}

	crawler.MarkCrawled("url", "hash", 200, 1, "", 0)
	crawler.MarkFailed("url", "hash", "error")
	crawler.AddPendingURL("url", "hash", 1, "", 1)

	stats := crawler.GetStatistics()
	if stats != nil {
		t.Error("GetStatistics should return nil when disabled")
	}

	crawler.Stop()
}

func TestIncrementalCrawler_ExpiredURL(t *testing.T) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".test_expired.json",
		MaxAge:           1 * time.Millisecond, // 非常短的过期时间
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	url := "https://example.com/expiring"
	hash := "expirehash"

	// 标记为已爬取
	crawler.MarkCrawled(url, hash, 200, 1, "", 0)

	// 等待过期
	time.Sleep(10 * time.Millisecond)

	// 应该需要重新爬取
	shouldCrawl, reason := crawler.ShouldCrawl(url, hash)
	if !shouldCrawl {
		t.Error("Expected shouldCrawl=true for expired URL")
	}
	if reason != "expired, recrawling" {
		t.Errorf("Expected reason='expired, recrawling', got '%s'", reason)
	}
}

func TestIncrementalStats(t *testing.T) {
	stats := &IncrementalStats{
		TotalCrawled:    100,
		TotalPending:    50,
		TotalFailed:     5,
		TotalSkipped:    20,
		TotalDiscovered: 175,
		BytesDownloaded: 1024000,
		StartTime:       time.Now().Add(-1 * time.Hour),
		LastCrawlTime:   time.Now().Add(-1 * time.Minute),
		LastSaveTime:    time.Now(),
	}

	if stats.TotalCrawled != 100 {
		t.Errorf("Expected TotalCrawled=100, got %d", stats.TotalCrawled)
	}
	if stats.TotalPending != 50 {
		t.Errorf("Expected TotalPending=50, got %d", stats.TotalPending)
	}
	if stats.BytesDownloaded != 1024000 {
		t.Errorf("Expected BytesDownloaded=1024000, got %d", stats.BytesDownloaded)
	}
}

func BenchmarkIncrementalCrawler_ShouldCrawl(b *testing.B) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".bench_should_crawl.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	// 预填充一些数据
	for i := 0; i < 10000; i++ {
		crawler.MarkCrawled("url", "existinghash"+string(rune(i)), 200, 1, "", 0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		crawler.ShouldCrawl("https://example.com/new", "newhash")
	}
}

func BenchmarkIncrementalCrawler_MarkCrawled(b *testing.B) {
	config := &IncrementalConfig{
		Enabled:          true,
		StateFile:        ".bench_mark_crawled.json",
		MaxAge:           1 * time.Hour,
		AutoSaveInterval: 0,
		CompressState:    false,
		MaxURLsInState:   b.N + 1000,
	}

	crawler := NewIncrementalCrawler(nil, config)
	crawler.Start("https://example.com")
	defer func() {
		crawler.Stop()
		os.Remove(crawler.stateFile)
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		crawler.MarkCrawled("url", "hash"+string(rune(i)), 200, 1, "", 1024)
	}
}
