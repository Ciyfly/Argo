// Package engine provides incremental crawling support
// P3优化: 增量爬取 - 支持断点续爬和状态持久化
package engine

import (
	"argo/pkg/log"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// IncrementalConfig 增量爬取配置
type IncrementalConfig struct {
	Enabled           bool          `yaml:"enabled" json:"enabled"`                       // 是否启用增量爬取
	StateFile         string        `yaml:"state_file" json:"state_file"`                 // 状态文件路径
	MaxAge            time.Duration `yaml:"max_age" json:"max_age"`                       // URL最大有效期,超过后需要重新爬取
	AutoSaveInterval  time.Duration `yaml:"auto_save_interval" json:"auto_save_interval"` // 自动保存间隔
	CompressState     bool          `yaml:"compress_state" json:"compress_state"`         // 是否压缩状态文件
	MaxURLsInState    int           `yaml:"max_urls_in_state" json:"max_urls_in_state"`   // 状态文件中最大URL数量
	CleanupOnStart    bool          `yaml:"cleanup_on_start" json:"cleanup_on_start"`     // 启动时是否清理过期URL
	BackupStateFile   bool          `yaml:"backup_state_file" json:"backup_state_file"`   // 保存前是否备份旧状态
	ResumeFromPending bool          `yaml:"resume_from_pending" json:"resume_from_pending"` // 是否从未完成的URL继续
}

// DefaultIncrementalConfig 返回默认增量爬取配置
func DefaultIncrementalConfig() *IncrementalConfig {
	return &IncrementalConfig{
		Enabled:           false,
		StateFile:         ".argo_crawl_state.json",
		MaxAge:            24 * time.Hour,
		AutoSaveInterval:  5 * time.Minute,
		CompressState:     true,
		MaxURLsInState:    1000000,
		CleanupOnStart:    true,
		BackupStateFile:   true,
		ResumeFromPending: true,
	}
}

// CrawledURLInfo 已爬取URL信息
type CrawledURLInfo struct {
	URL          string    `json:"url"`                     // URL地址
	Hash         string    `json:"hash"`                    // 规范化后的hash
	CrawledAt    time.Time `json:"crawled_at"`              // 爬取时间
	StatusCode   int       `json:"status_code,omitempty"`   // 响应状态码
	ContentHash  string    `json:"content_hash,omitempty"`  // 内容hash,用于检测变化
	Depth        int       `json:"depth"`                   // 爬取深度
	SourceURL    string    `json:"source_url,omitempty"`    // 来源URL
	ResponseSize int64     `json:"response_size,omitempty"` // 响应大小
	CrawlCount   int       `json:"crawl_count"`             // 爬取次数
}

// PendingURLInfo 待爬取URL信息
type PendingURLInfo struct {
	URL       string    `json:"url"`        // URL地址
	Hash      string    `json:"hash"`       // 规范化后的hash
	AddedAt   time.Time `json:"added_at"`   // 添加时间
	Priority  int       `json:"priority"`   // 优先级
	Depth     int       `json:"depth"`      // 深度
	SourceURL string    `json:"source_url"` // 来源URL
	Retries   int       `json:"retries"`    // 重试次数
}

// CrawlState 爬取状态
type CrawlState struct {
	Version      string                    `json:"version"`       // 状态文件版本
	Target       string                    `json:"target"`        // 目标站点
	StartedAt    time.Time                 `json:"started_at"`    // 开始时间
	LastSavedAt  time.Time                 `json:"last_saved_at"` // 最后保存时间
	LastCrawlAt  time.Time                 `json:"last_crawl_at"` // 最后爬取时间
	CrawledURLs  map[string]*CrawledURLInfo `json:"crawled_urls"`  // 已爬取URL (key: hash)
	PendingURLs  map[string]*PendingURLInfo `json:"pending_urls"`  // 待爬取URL (key: hash)
	FailedURLs   map[string]*FailedURLInfo  `json:"failed_urls"`   // 失败URL (key: hash)
	Statistics   CrawlStatistics           `json:"statistics"`    // 爬取统计
	Checkpoints  []Checkpoint              `json:"checkpoints"`   // 检查点列表
}

// FailedURLInfo 失败URL信息
type FailedURLInfo struct {
	URL        string    `json:"url"`
	Hash       string    `json:"hash"`
	FailedAt   time.Time `json:"failed_at"`
	Reason     string    `json:"reason"`
	RetryCount int       `json:"retry_count"`
	LastError  string    `json:"last_error"`
}

// CrawlStatistics 爬取统计
type CrawlStatistics struct {
	TotalURLsDiscovered int64         `json:"total_urls_discovered"` // 发现的总URL数
	TotalURLsCrawled    int64         `json:"total_urls_crawled"`    // 已爬取URL数
	TotalURLsSkipped    int64         `json:"total_urls_skipped"`    // 跳过的URL数(已存在)
	TotalURLsFailed     int64         `json:"total_urls_failed"`     // 失败的URL数
	TotalBytesDownloaded int64        `json:"total_bytes_downloaded"` // 下载的总字节数
	AverageResponseTime time.Duration `json:"average_response_time"` // 平均响应时间
	StartTime           time.Time     `json:"start_time"`            // 开始时间
	LastUpdateTime      time.Time     `json:"last_update_time"`      // 最后更新时间
}

// Checkpoint 检查点
type Checkpoint struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	URLsCrawled int64     `json:"urls_crawled"`
	URLsPending int64     `json:"urls_pending"`
	Description string    `json:"description"`
}

// IncrementalCrawler 增量爬取器
type IncrementalCrawler struct {
	config     *IncrementalConfig
	state      *CrawlState
	stateFile  string
	mu         sync.RWMutex
	dirty      bool          // 是否有未保存的变更
	stopChan   chan struct{} // 停止信号
	saveChan   chan struct{} // 保存信号
	engine     *EngineInfo
}

// NewIncrementalCrawler 创建增量爬取器
func NewIncrementalCrawler(engine *EngineInfo, config *IncrementalConfig) *IncrementalCrawler {
	if config == nil {
		config = DefaultIncrementalConfig()
	}

	ic := &IncrementalCrawler{
		config:    config,
		engine:    engine,
		stateFile: config.StateFile,
		stopChan:  make(chan struct{}),
		saveChan:  make(chan struct{}, 1),
	}

	return ic
}

// Start 启动增量爬取器
func (ic *IncrementalCrawler) Start(target string) error {
	if !ic.config.Enabled {
		log.Logger.Debug("incremental crawler disabled")
		return nil
	}

	// 构建状态文件路径
	ic.stateFile = ic.buildStateFilePath(target)

	// 尝试加载已有状态
	if err := ic.loadState(); err != nil {
		log.Logger.Warnf("load crawl state failed, starting fresh: %v", err)
		ic.initNewState(target)
	} else {
		log.Logger.Infof("loaded crawl state: %d crawled, %d pending URLs",
			len(ic.state.CrawledURLs), len(ic.state.PendingURLs))
	}

	// 清理过期URL
	if ic.config.CleanupOnStart {
		cleaned := ic.cleanupExpiredURLs()
		if cleaned > 0 {
			log.Logger.Infof("cleaned %d expired URLs", cleaned)
		}
	}

	// 启动自动保存协程
	go ic.autoSaveLoop()

	return nil
}

// Stop 停止增量爬取器
func (ic *IncrementalCrawler) Stop() error {
	if !ic.config.Enabled {
		return nil
	}

	close(ic.stopChan)

	// 最终保存状态
	if err := ic.SaveState(); err != nil {
		log.Logger.Errorf("final state save failed: %v", err)
		return err
	}

	log.Logger.Info("incremental crawler stopped, state saved")
	return nil
}

// buildStateFilePath 构建状态文件路径
func (ic *IncrementalCrawler) buildStateFilePath(target string) string {
	if ic.config.StateFile != "" && filepath.IsAbs(ic.config.StateFile) {
		return ic.config.StateFile
	}

	// 根据目标URL生成文件名
	u, err := url.Parse(target)
	if err != nil {
		return ic.config.StateFile
	}

	// 清理host名用于文件名
	host := strings.ReplaceAll(u.Host, ":", "_")
	host = strings.ReplaceAll(host, ".", "_")

	filename := fmt.Sprintf(".argo_state_%s.json", host)
	if ic.config.CompressState {
		filename += ".gz"
	}

	return filename
}

// initNewState 初始化新状态
func (ic *IncrementalCrawler) initNewState(target string) {
	ic.state = &CrawlState{
		Version:     "1.0",
		Target:      target,
		StartedAt:   time.Now(),
		CrawledURLs: make(map[string]*CrawledURLInfo),
		PendingURLs: make(map[string]*PendingURLInfo),
		FailedURLs:  make(map[string]*FailedURLInfo),
		Statistics: CrawlStatistics{
			StartTime: time.Now(),
		},
		Checkpoints: make([]Checkpoint, 0),
	}
}

// loadState 加载状态
func (ic *IncrementalCrawler) loadState() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	// 检查文件是否存在
	if _, err := os.Stat(ic.stateFile); os.IsNotExist(err) {
		return fmt.Errorf("state file not found: %s", ic.stateFile)
	}

	file, err := os.Open(ic.stateFile)
	if err != nil {
		return fmt.Errorf("open state file: %w", err)
	}
	defer file.Close()

	var reader io.Reader = file

	// 如果是压缩文件,使用gzip解压
	if strings.HasSuffix(ic.stateFile, ".gz") {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	ic.state = &CrawlState{}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(ic.state); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}

	// 初始化可能为nil的map
	if ic.state.CrawledURLs == nil {
		ic.state.CrawledURLs = make(map[string]*CrawledURLInfo)
	}
	if ic.state.PendingURLs == nil {
		ic.state.PendingURLs = make(map[string]*PendingURLInfo)
	}
	if ic.state.FailedURLs == nil {
		ic.state.FailedURLs = make(map[string]*FailedURLInfo)
	}

	return nil
}

// SaveState 保存状态
func (ic *IncrementalCrawler) SaveState() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.state == nil {
		return nil
	}

	// 备份旧状态文件
	if ic.config.BackupStateFile {
		if _, err := os.Stat(ic.stateFile); err == nil {
			backupFile := ic.stateFile + ".bak"
			os.Rename(ic.stateFile, backupFile)
		}
	}

	// 更新保存时间
	ic.state.LastSavedAt = time.Now()
	ic.state.Statistics.LastUpdateTime = time.Now()

	// 创建临时文件
	tmpFile := ic.stateFile + ".tmp"
	file, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	var writer io.Writer = file
	var gzWriter *gzip.Writer

	// 如果启用压缩
	if ic.config.CompressState && strings.HasSuffix(ic.stateFile, ".gz") {
		gzWriter = gzip.NewWriter(file)
		writer = gzWriter
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(ic.state); err != nil {
		file.Close()
		if gzWriter != nil {
			gzWriter.Close()
		}
		os.Remove(tmpFile)
		return fmt.Errorf("encode state: %w", err)
	}

	if gzWriter != nil {
		if err := gzWriter.Close(); err != nil {
			file.Close()
			os.Remove(tmpFile)
			return fmt.Errorf("close gzip writer: %w", err)
		}
	}

	if err := file.Close(); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("close file: %w", err)
	}

	// 原子替换
	if err := os.Rename(tmpFile, ic.stateFile); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("rename temp file: %w", err)
	}

	ic.dirty = false
	log.Logger.Debugf("crawl state saved: %d crawled, %d pending",
		len(ic.state.CrawledURLs), len(ic.state.PendingURLs))

	return nil
}

// autoSaveLoop 自动保存循环
func (ic *IncrementalCrawler) autoSaveLoop() {
	if ic.config.AutoSaveInterval <= 0 {
		return
	}

	ticker := time.NewTicker(ic.config.AutoSaveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ic.stopChan:
			return
		case <-ticker.C:
			if ic.dirty {
				if err := ic.SaveState(); err != nil {
					log.Logger.Errorf("auto save state failed: %v", err)
				}
			}
		case <-ic.saveChan:
			if err := ic.SaveState(); err != nil {
				log.Logger.Errorf("triggered save state failed: %v", err)
			}
		}
	}
}

// TriggerSave 触发保存
func (ic *IncrementalCrawler) TriggerSave() {
	select {
	case ic.saveChan <- struct{}{}:
	default:
	}
}

// ShouldCrawl 判断URL是否应该爬取
func (ic *IncrementalCrawler) ShouldCrawl(urlStr string, hash string) (bool, string) {
	if !ic.config.Enabled || ic.state == nil {
		return true, ""
	}

	ic.mu.RLock()
	defer ic.mu.RUnlock()

	// 检查是否已爬取
	if info, exists := ic.state.CrawledURLs[hash]; exists {
		// 检查是否过期
		if time.Since(info.CrawledAt) < ic.config.MaxAge {
			return false, fmt.Sprintf("already crawled at %s", info.CrawledAt.Format(time.RFC3339))
		}
		// 已过期,需要重新爬取
		return true, "expired, recrawling"
	}

	// 检查是否在失败列表中
	if info, exists := ic.state.FailedURLs[hash]; exists {
		// 失败次数过多则跳过
		if info.RetryCount >= 3 {
			return false, fmt.Sprintf("failed %d times: %s", info.RetryCount, info.LastError)
		}
	}

	return true, ""
}

// MarkCrawled 标记URL已爬取
func (ic *IncrementalCrawler) MarkCrawled(urlStr, hash string, statusCode int, depth int, sourceURL string, responseSize int64) {
	if !ic.config.Enabled || ic.state == nil {
		return
	}

	ic.mu.Lock()
	defer ic.mu.Unlock()

	info, exists := ic.state.CrawledURLs[hash]
	if !exists {
		info = &CrawledURLInfo{
			URL:  urlStr,
			Hash: hash,
		}
		ic.state.CrawledURLs[hash] = info
		ic.state.Statistics.TotalURLsCrawled++
	}

	info.CrawledAt = time.Now()
	info.StatusCode = statusCode
	info.Depth = depth
	info.SourceURL = sourceURL
	info.ResponseSize = responseSize
	info.CrawlCount++

	// 从待爬取和失败列表中移除
	delete(ic.state.PendingURLs, hash)
	delete(ic.state.FailedURLs, hash)

	// 更新统计
	ic.state.LastCrawlAt = time.Now()
	ic.state.Statistics.TotalBytesDownloaded += responseSize

	ic.dirty = true

	// 检查是否需要限制状态大小
	ic.checkAndTrimState()
}

// MarkSkipped 标记URL被跳过
func (ic *IncrementalCrawler) MarkSkipped(hash string) {
	if !ic.config.Enabled || ic.state == nil {
		return
	}

	ic.mu.Lock()
	ic.state.Statistics.TotalURLsSkipped++
	ic.mu.Unlock()
}

// MarkFailed 标记URL爬取失败
func (ic *IncrementalCrawler) MarkFailed(urlStr, hash string, reason string) {
	if !ic.config.Enabled || ic.state == nil {
		return
	}

	ic.mu.Lock()
	defer ic.mu.Unlock()

	info, exists := ic.state.FailedURLs[hash]
	if !exists {
		info = &FailedURLInfo{
			URL:  urlStr,
			Hash: hash,
		}
		ic.state.FailedURLs[hash] = info
		ic.state.Statistics.TotalURLsFailed++
	}

	info.FailedAt = time.Now()
	info.Reason = reason
	info.LastError = reason
	info.RetryCount++

	// 从待爬取列表中移除
	delete(ic.state.PendingURLs, hash)

	ic.dirty = true
}

// AddPendingURL 添加待爬取URL
func (ic *IncrementalCrawler) AddPendingURL(urlStr, hash string, depth int, sourceURL string, priority int) {
	if !ic.config.Enabled || ic.state == nil {
		return
	}

	ic.mu.Lock()
	defer ic.mu.Unlock()

	// 如果已经爬取过,跳过
	if _, exists := ic.state.CrawledURLs[hash]; exists {
		return
	}

	// 如果已在待爬取列表,更新优先级
	if info, exists := ic.state.PendingURLs[hash]; exists {
		if priority > info.Priority {
			info.Priority = priority
		}
		return
	}

	ic.state.PendingURLs[hash] = &PendingURLInfo{
		URL:       urlStr,
		Hash:      hash,
		AddedAt:   time.Now(),
		Priority:  priority,
		Depth:     depth,
		SourceURL: sourceURL,
	}

	ic.state.Statistics.TotalURLsDiscovered++
	ic.dirty = true
}

// GetPendingURLs 获取待爬取URL列表
func (ic *IncrementalCrawler) GetPendingURLs() []*PendingURLInfo {
	if !ic.config.Enabled || ic.state == nil {
		return nil
	}

	ic.mu.RLock()
	defer ic.mu.RUnlock()

	urls := make([]*PendingURLInfo, 0, len(ic.state.PendingURLs))
	for _, info := range ic.state.PendingURLs {
		urls = append(urls, info)
	}

	// 按优先级排序
	sort.Slice(urls, func(i, j int) bool {
		if urls[i].Priority != urls[j].Priority {
			return urls[i].Priority > urls[j].Priority
		}
		return urls[i].AddedAt.Before(urls[j].AddedAt)
	})

	return urls
}

// cleanupExpiredURLs 清理过期URL
func (ic *IncrementalCrawler) cleanupExpiredURLs() int {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.state == nil {
		return 0
	}

	cleaned := 0
	now := time.Now()

	for hash, info := range ic.state.CrawledURLs {
		if now.Sub(info.CrawledAt) > ic.config.MaxAge*2 {
			delete(ic.state.CrawledURLs, hash)
			cleaned++
		}
	}

	// 清理过旧的失败记录
	for hash, info := range ic.state.FailedURLs {
		if now.Sub(info.FailedAt) > ic.config.MaxAge {
			delete(ic.state.FailedURLs, hash)
			cleaned++
		}
	}

	if cleaned > 0 {
		ic.dirty = true
	}

	return cleaned
}

// checkAndTrimState 检查并裁剪状态大小
func (ic *IncrementalCrawler) checkAndTrimState() {
	if ic.config.MaxURLsInState <= 0 {
		return
	}

	totalURLs := len(ic.state.CrawledURLs)
	if totalURLs <= ic.config.MaxURLsInState {
		return
	}

	// 需要删除的数量
	toRemove := totalURLs - ic.config.MaxURLsInState

	// 按时间排序,删除最旧的
	type urlTime struct {
		hash      string
		crawledAt time.Time
	}

	urls := make([]urlTime, 0, totalURLs)
	for hash, info := range ic.state.CrawledURLs {
		urls = append(urls, urlTime{hash: hash, crawledAt: info.CrawledAt})
	}

	sort.Slice(urls, func(i, j int) bool {
		return urls[i].crawledAt.Before(urls[j].crawledAt)
	})

	for i := 0; i < toRemove && i < len(urls); i++ {
		delete(ic.state.CrawledURLs, urls[i].hash)
	}

	log.Logger.Debugf("trimmed %d old URLs from state", toRemove)
}

// CreateCheckpoint 创建检查点
func (ic *IncrementalCrawler) CreateCheckpoint(description string) string {
	if !ic.config.Enabled || ic.state == nil {
		return ""
	}

	ic.mu.Lock()
	defer ic.mu.Unlock()

	checkpoint := Checkpoint{
		ID:          fmt.Sprintf("cp_%d", time.Now().UnixNano()),
		CreatedAt:   time.Now(),
		URLsCrawled: int64(len(ic.state.CrawledURLs)),
		URLsPending: int64(len(ic.state.PendingURLs)),
		Description: description,
	}

	ic.state.Checkpoints = append(ic.state.Checkpoints, checkpoint)

	// 只保留最近10个检查点
	if len(ic.state.Checkpoints) > 10 {
		ic.state.Checkpoints = ic.state.Checkpoints[len(ic.state.Checkpoints)-10:]
	}

	ic.dirty = true

	return checkpoint.ID
}

// GetStatistics 获取统计信息
func (ic *IncrementalCrawler) GetStatistics() *IncrementalStats {
	if !ic.config.Enabled || ic.state == nil {
		return nil
	}

	ic.mu.RLock()
	defer ic.mu.RUnlock()

	return &IncrementalStats{
		TotalCrawled:   int64(len(ic.state.CrawledURLs)),
		TotalPending:   int64(len(ic.state.PendingURLs)),
		TotalFailed:    int64(len(ic.state.FailedURLs)),
		TotalSkipped:   ic.state.Statistics.TotalURLsSkipped,
		TotalDiscovered: ic.state.Statistics.TotalURLsDiscovered,
		BytesDownloaded: ic.state.Statistics.TotalBytesDownloaded,
		StartTime:      ic.state.StartedAt,
		LastCrawlTime:  ic.state.LastCrawlAt,
		LastSaveTime:   ic.state.LastSavedAt,
	}
}

// IncrementalStats 增量爬取统计
type IncrementalStats struct {
	TotalCrawled    int64     `json:"total_crawled"`
	TotalPending    int64     `json:"total_pending"`
	TotalFailed     int64     `json:"total_failed"`
	TotalSkipped    int64     `json:"total_skipped"`
	TotalDiscovered int64     `json:"total_discovered"`
	BytesDownloaded int64     `json:"bytes_downloaded"`
	StartTime       time.Time `json:"start_time"`
	LastCrawlTime   time.Time `json:"last_crawl_time"`
	LastSaveTime    time.Time `json:"last_save_time"`
}

// HasPendingURLs 是否有待爬取URL
func (ic *IncrementalCrawler) HasPendingURLs() bool {
	if !ic.config.Enabled || ic.state == nil {
		return false
	}

	ic.mu.RLock()
	defer ic.mu.RUnlock()

	return len(ic.state.PendingURLs) > 0
}

// GetCrawledCount 获取已爬取数量
func (ic *IncrementalCrawler) GetCrawledCount() int {
	if !ic.config.Enabled || ic.state == nil {
		return 0
	}

	ic.mu.RLock()
	defer ic.mu.RUnlock()

	return len(ic.state.CrawledURLs)
}

// IsCrawled 检查URL是否已爬取
func (ic *IncrementalCrawler) IsCrawled(hash string) bool {
	if !ic.config.Enabled || ic.state == nil {
		return false
	}

	ic.mu.RLock()
	defer ic.mu.RUnlock()

	_, exists := ic.state.CrawledURLs[hash]
	return exists
}

// Clear 清空状态
func (ic *IncrementalCrawler) Clear() {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.state != nil {
		ic.state.CrawledURLs = make(map[string]*CrawledURLInfo)
		ic.state.PendingURLs = make(map[string]*PendingURLInfo)
		ic.state.FailedURLs = make(map[string]*FailedURLInfo)
		ic.state.Statistics = CrawlStatistics{StartTime: time.Now()}
		ic.dirty = true
	}
}

// ExportState 导出状态为JSON
func (ic *IncrementalCrawler) ExportState() ([]byte, error) {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	if ic.state == nil {
		return nil, fmt.Errorf("no state to export")
	}

	return json.MarshalIndent(ic.state, "", "  ")
}

// ImportState 从JSON导入状态
func (ic *IncrementalCrawler) ImportState(data []byte) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	newState := &CrawlState{}
	if err := json.Unmarshal(data, newState); err != nil {
		return fmt.Errorf("unmarshal state: %w", err)
	}

	ic.state = newState
	ic.dirty = true

	return nil
}

// MergeState 合并另一个状态
func (ic *IncrementalCrawler) MergeState(other *CrawlState) {
	if other == nil {
		return
	}

	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.state == nil {
		ic.state = other
		return
	}

	// 合并已爬取URL
	for hash, info := range other.CrawledURLs {
		if existing, exists := ic.state.CrawledURLs[hash]; !exists || info.CrawledAt.After(existing.CrawledAt) {
			ic.state.CrawledURLs[hash] = info
		}
	}

	// 合并待爬取URL (不覆盖已爬取的)
	for hash, info := range other.PendingURLs {
		if _, crawled := ic.state.CrawledURLs[hash]; !crawled {
			if _, exists := ic.state.PendingURLs[hash]; !exists {
				ic.state.PendingURLs[hash] = info
			}
		}
	}

	ic.dirty = true
}
