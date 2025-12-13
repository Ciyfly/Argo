package engine

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"argo/pkg/log"
)

// DistributedQueue 分布式任务队列接口
type DistributedQueue interface {
	// 任务操作
	Push(task *CrawlTask) error
	Pop(ctx context.Context) (*CrawlTask, error)
	Ack(taskID string) error
	Nack(taskID string) error

	// 队列状态
	Len() (int64, error)
	Close() error
}

// DistributedResultStore 分布式结果存储接口
type DistributedResultStore interface {
	// 结果操作
	Store(result *CrawlResult) error
	Get(taskID string) (*CrawlResult, error)
	List(filter ResultFilter) ([]*CrawlResult, error)

	// 聚合操作
	Aggregate(target string) (*AggregatedResult, error)
	Close() error
}

// CrawlTask 爬取任务
type CrawlTask struct {
	ID          string            `json:"id"`
	URL         string            `json:"url"`
	Target      string            `json:"target"` // 目标站点
	Depth       int               `json:"depth"`
	Priority    int               `json:"priority"`
	Retries     int               `json:"retries"`
	MaxRetries  int               `json:"max_retries"`
	SourceType  string            `json:"source_type"`
	SourceURL   string            `json:"source_url"`
	Metadata    map[string]string `json:"metadata"`
	CreatedAt   time.Time         `json:"created_at"`
	ScheduledAt time.Time         `json:"scheduled_at"`
	WorkerID    string            `json:"worker_id,omitempty"`
	Status      TaskStatus        `json:"status"`
}

// TaskStatus 任务状态
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusProcessing TaskStatus = "processing"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusRetrying   TaskStatus = "retrying"
)

// CrawlResult 爬取结果
type CrawlResult struct {
	TaskID        string            `json:"task_id"`
	URL           string            `json:"url"`
	Target        string            `json:"target"`
	StatusCode    int               `json:"status_code"`
	ContentType   string            `json:"content_type"`
	ResponseSize  int               `json:"response_size"`
	LinksFound    []string          `json:"links_found"`
	FormsFound    int               `json:"forms_found"`
	APIEndpoints  []string          `json:"api_endpoints,omitempty"`
	Errors        []string          `json:"errors,omitempty"`
	Metadata      map[string]string `json:"metadata"`
	ProcessedAt   time.Time         `json:"processed_at"`
	ProcessTimeMs int64             `json:"process_time_ms"`
	WorkerID      string            `json:"worker_id"`
}

// ResultFilter 结果过滤器
type ResultFilter struct {
	Target     string
	StatusCode int
	Since      time.Time
	Until      time.Time
	Limit      int
	Offset     int
}

// AggregatedResult 聚合结果
type AggregatedResult struct {
	Target           string                 `json:"target"`
	TotalURLs        int64                  `json:"total_urls"`
	SuccessCount     int64                  `json:"success_count"`
	FailedCount      int64                  `json:"failed_count"`
	UniqueLinks      int64                  `json:"unique_links"`
	UniqueAPIs       int64                  `json:"unique_apis"`
	AvgProcessTimeMs int64                  `json:"avg_process_time_ms"`
	StatusCodes      map[int]int64          `json:"status_codes"`
	ContentTypes     map[string]int64       `json:"content_types"`
	TopPaths         []PathCount            `json:"top_paths"`
	StartTime        time.Time              `json:"start_time"`
	EndTime          time.Time              `json:"end_time"`
	Extra            map[string]interface{} `json:"extra,omitempty"`
}

// PathCount 路径计数
type PathCount struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// MemoryQueue 内存队列实现（单机模式）
type MemoryQueue struct {
	mu       sync.Mutex
	tasks    []*CrawlTask
	pending  map[string]*CrawlTask
	maxSize  int
	closed   int32
}

// NewMemoryQueue 创建内存队列
func NewMemoryQueue(maxSize int) *MemoryQueue {
	if maxSize <= 0 {
		maxSize = 100000
	}
	return &MemoryQueue{
		tasks:   make([]*CrawlTask, 0, 1000),
		pending: make(map[string]*CrawlTask),
		maxSize: maxSize,
	}
}

// Push 添加任务
func (q *MemoryQueue) Push(task *CrawlTask) error {
	if atomic.LoadInt32(&q.closed) == 1 {
		return context.Canceled
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.tasks) >= q.maxSize {
		return ErrQueueFull
	}

	task.Status = TaskStatusPending
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}

	// 按优先级插入
	inserted := false
	for i, t := range q.tasks {
		if task.Priority > t.Priority {
			// 插入到当前位置
			q.tasks = append(q.tasks[:i], append([]*CrawlTask{task}, q.tasks[i:]...)...)
			inserted = true
			break
		}
	}
	if !inserted {
		q.tasks = append(q.tasks, task)
	}

	return nil
}

// Pop 获取任务
func (q *MemoryQueue) Pop(ctx context.Context) (*CrawlTask, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if atomic.LoadInt32(&q.closed) == 1 {
			return nil, context.Canceled
		}

		q.mu.Lock()
		if len(q.tasks) > 0 {
			task := q.tasks[0]
			q.tasks = q.tasks[1:]
			task.Status = TaskStatusProcessing
			task.ScheduledAt = time.Now()
			q.pending[task.ID] = task
			q.mu.Unlock()
			return task, nil
		}
		q.mu.Unlock()

		time.Sleep(100 * time.Millisecond)
	}
}

// Ack 确认任务完成
func (q *MemoryQueue) Ack(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.pending, taskID)
	return nil
}

// Nack 任务失败，重新入队
func (q *MemoryQueue) Nack(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	task, ok := q.pending[taskID]
	if !ok {
		return nil
	}
	delete(q.pending, taskID)

	task.Retries++
	if task.Retries <= task.MaxRetries {
		task.Status = TaskStatusRetrying
		q.tasks = append(q.tasks, task)
	} else {
		task.Status = TaskStatusFailed
	}

	return nil
}

// Len 获取队列长度
func (q *MemoryQueue) Len() (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return int64(len(q.tasks)), nil
}

// Close 关闭队列
func (q *MemoryQueue) Close() error {
	atomic.StoreInt32(&q.closed, 1)
	return nil
}

// ErrQueueFull 队列满错误
var ErrQueueFull = &QueueError{Message: "queue is full"}

// QueueError 队列错误
type QueueError struct {
	Message string
}

func (e *QueueError) Error() string {
	return e.Message
}

// MemoryResultStore 内存结果存储
type MemoryResultStore struct {
	mu      sync.RWMutex
	results map[string]*CrawlResult
	byTarget map[string][]string // target -> taskIDs
}

// NewMemoryResultStore 创建内存结果存储
func NewMemoryResultStore() *MemoryResultStore {
	return &MemoryResultStore{
		results:  make(map[string]*CrawlResult),
		byTarget: make(map[string][]string),
	}
}

// Store 存储结果
func (s *MemoryResultStore) Store(result *CrawlResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.results[result.TaskID] = result
	s.byTarget[result.Target] = append(s.byTarget[result.Target], result.TaskID)
	return nil
}

// Get 获取结果
func (s *MemoryResultStore) Get(taskID string) (*CrawlResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result, ok := s.results[taskID]
	if !ok {
		return nil, nil
	}
	return result, nil
}

// List 列出结果
func (s *MemoryResultStore) List(filter ResultFilter) ([]*CrawlResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*CrawlResult

	taskIDs := s.byTarget[filter.Target]
	if filter.Target == "" {
		for id := range s.results {
			taskIDs = append(taskIDs, id)
		}
	}

	for _, id := range taskIDs {
		result := s.results[id]
		if result == nil {
			continue
		}

		// 应用过滤条件
		if filter.StatusCode > 0 && result.StatusCode != filter.StatusCode {
			continue
		}
		if !filter.Since.IsZero() && result.ProcessedAt.Before(filter.Since) {
			continue
		}
		if !filter.Until.IsZero() && result.ProcessedAt.After(filter.Until) {
			continue
		}

		results = append(results, result)
	}

	// 应用分页
	if filter.Offset > 0 && filter.Offset < len(results) {
		results = results[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(results) {
		results = results[:filter.Limit]
	}

	return results, nil
}

// Aggregate 聚合结果
func (s *MemoryResultStore) Aggregate(target string) (*AggregatedResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agg := &AggregatedResult{
		Target:       target,
		StatusCodes:  make(map[int]int64),
		ContentTypes: make(map[string]int64),
	}

	taskIDs := s.byTarget[target]
	if len(taskIDs) == 0 {
		return agg, nil
	}

	uniqueLinks := make(map[string]bool)
	uniqueAPIs := make(map[string]bool)
	var totalProcessTime int64

	for _, id := range taskIDs {
		result := s.results[id]
		if result == nil {
			continue
		}

		agg.TotalURLs++

		if result.StatusCode >= 200 && result.StatusCode < 400 {
			agg.SuccessCount++
		} else if result.StatusCode >= 400 {
			agg.FailedCount++
		}

		agg.StatusCodes[result.StatusCode]++
		if result.ContentType != "" {
			agg.ContentTypes[result.ContentType]++
		}

		for _, link := range result.LinksFound {
			uniqueLinks[link] = true
		}
		for _, api := range result.APIEndpoints {
			uniqueAPIs[api] = true
		}

		totalProcessTime += result.ProcessTimeMs

		if agg.StartTime.IsZero() || result.ProcessedAt.Before(agg.StartTime) {
			agg.StartTime = result.ProcessedAt
		}
		if result.ProcessedAt.After(agg.EndTime) {
			agg.EndTime = result.ProcessedAt
		}
	}

	agg.UniqueLinks = int64(len(uniqueLinks))
	agg.UniqueAPIs = int64(len(uniqueAPIs))

	if agg.TotalURLs > 0 {
		agg.AvgProcessTimeMs = totalProcessTime / agg.TotalURLs
	}

	return agg, nil
}

// Close 关闭存储
func (s *MemoryResultStore) Close() error {
	return nil
}

// DistributedCoordinator 分布式协调器
type DistributedCoordinator struct {
	mu          sync.RWMutex
	workerID    string
	queue       DistributedQueue
	resultStore DistributedResultStore
	engine      *EngineInfo
	config      *DistributedConfig

	// 工作节点状态
	workers     map[string]*WorkerInfo
	isLeader    bool

	// 控制
	ctx         context.Context
	cancel      context.CancelFunc
	running     int32
}

// DistributedConfig 分布式配置
type DistributedConfig struct {
	WorkerID       string
	QueueType      string // memory, redis, kafka
	ResultStoreType string // memory, mongodb, elasticsearch
	HeartbeatInterval time.Duration
	TaskTimeout    time.Duration
	MaxWorkers     int
}

// WorkerInfo 工作节点信息
type WorkerInfo struct {
	ID            string    `json:"id"`
	Status        string    `json:"status"` // active, idle, offline
	CurrentTask   string    `json:"current_task,omitempty"`
	TasksCompleted int64    `json:"tasks_completed"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	StartTime     time.Time `json:"start_time"`
}

// DefaultDistributedConfig 默认配置
func DefaultDistributedConfig() *DistributedConfig {
	return &DistributedConfig{
		WorkerID:          generateWorkerID(),
		QueueType:         "memory",
		ResultStoreType:   "memory",
		HeartbeatInterval: 10 * time.Second,
		TaskTimeout:       5 * time.Minute,
		MaxWorkers:        10,
	}
}

// NewDistributedCoordinator 创建分布式协调器
func NewDistributedCoordinator(engine *EngineInfo, config *DistributedConfig) *DistributedCoordinator {
	if config == nil {
		config = DefaultDistributedConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	dc := &DistributedCoordinator{
		workerID:    config.WorkerID,
		engine:      engine,
		config:      config,
		workers:     make(map[string]*WorkerInfo),
		ctx:         ctx,
		cancel:      cancel,
	}

	// 初始化队列
	switch config.QueueType {
	case "memory":
		dc.queue = NewMemoryQueue(100000)
	// TODO: 支持 Redis, Kafka 等
	default:
		dc.queue = NewMemoryQueue(100000)
	}

	// 初始化结果存储
	switch config.ResultStoreType {
	case "memory":
		dc.resultStore = NewMemoryResultStore()
	// TODO: 支持 MongoDB, Elasticsearch 等
	default:
		dc.resultStore = NewMemoryResultStore()
	}

	return dc
}

// Start 启动协调器
func (dc *DistributedCoordinator) Start() error {
	if !atomic.CompareAndSwapInt32(&dc.running, 0, 1) {
		return nil
	}

	// 注册当前工作节点
	dc.registerWorker()

	// 启动心跳
	go dc.heartbeatLoop()

	// 启动任务处理
	go dc.processLoop()

	log.Logger.Infof("DistributedCoordinator started: workerID=%s", dc.workerID)
	return nil
}

// Stop 停止协调器
func (dc *DistributedCoordinator) Stop() {
	if atomic.CompareAndSwapInt32(&dc.running, 1, 0) {
		dc.cancel()
		dc.queue.Close()
		dc.resultStore.Close()
		log.Logger.Info("DistributedCoordinator stopped")
	}
}

// SubmitTask 提交任务
func (dc *DistributedCoordinator) SubmitTask(task *CrawlTask) error {
	if task.ID == "" {
		task.ID = generateTaskID()
	}
	return dc.queue.Push(task)
}

// SubmitURL 从URL创建并提交任务
func (dc *DistributedCoordinator) SubmitURL(urlInfo *UrlInfo, target string) error {
	task := &CrawlTask{
		ID:         generateTaskID(),
		URL:        urlInfo.Url,
		Target:     target,
		Depth:      urlInfo.Depth,
		Priority:   50,
		MaxRetries: 2,
		SourceType: urlInfo.SourceType,
		SourceURL:  urlInfo.SourceUrl,
		Metadata:   make(map[string]string),
		CreatedAt:  time.Now(),
	}

	return dc.queue.Push(task)
}

// GetResult 获取结果
func (dc *DistributedCoordinator) GetResult(taskID string) (*CrawlResult, error) {
	return dc.resultStore.Get(taskID)
}

// GetAggregatedResult 获取聚合结果
func (dc *DistributedCoordinator) GetAggregatedResult(target string) (*AggregatedResult, error) {
	return dc.resultStore.Aggregate(target)
}

// processLoop 任务处理循环
func (dc *DistributedCoordinator) processLoop() {
	for {
		select {
		case <-dc.ctx.Done():
			return
		default:
		}

		task, err := dc.queue.Pop(dc.ctx)
		if err != nil {
			if err != context.Canceled {
				log.Logger.Debugf("DistributedCoordinator: pop error: %v", err)
			}
			continue
		}

		task.WorkerID = dc.workerID
		dc.updateWorkerStatus("processing", task.ID)

		// 处理任务
		result := dc.processTask(task)

		// 存储结果
		if err := dc.resultStore.Store(result); err != nil {
			log.Logger.Warnf("DistributedCoordinator: store result error: %v", err)
		}

		// 确认完成
		if len(result.Errors) == 0 {
			dc.queue.Ack(task.ID)
		} else {
			dc.queue.Nack(task.ID)
		}

		dc.updateWorkerStatus("idle", "")
	}
}

// processTask 处理单个任务
func (dc *DistributedCoordinator) processTask(task *CrawlTask) *CrawlResult {
	startTime := time.Now()

	result := &CrawlResult{
		TaskID:      task.ID,
		URL:         task.URL,
		Target:      task.Target,
		Metadata:    make(map[string]string),
		ProcessedAt: time.Now(),
		WorkerID:    dc.workerID,
	}

	// 通过引擎处理URL
	if dc.engine != nil {
		urlInfo := &UrlInfo{
			Url:        task.URL,
			Depth:      task.Depth,
			SourceType: task.SourceType,
			SourceUrl:  task.SourceURL,
		}

		// 这里简化处理，实际应该调用引擎的爬取逻辑
		dc.engine.PushStaticUrl(urlInfo)
		result.StatusCode = 200
	}

	result.ProcessTimeMs = time.Since(startTime).Milliseconds()
	return result
}

// heartbeatLoop 心跳循环
func (dc *DistributedCoordinator) heartbeatLoop() {
	ticker := time.NewTicker(dc.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-dc.ctx.Done():
			return
		case <-ticker.C:
			dc.sendHeartbeat()
			dc.checkWorkerHealth()
		}
	}
}

// registerWorker 注册工作节点
func (dc *DistributedCoordinator) registerWorker() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	dc.workers[dc.workerID] = &WorkerInfo{
		ID:            dc.workerID,
		Status:        "active",
		LastHeartbeat: time.Now(),
		StartTime:     time.Now(),
	}
}

// updateWorkerStatus 更新工作节点状态
func (dc *DistributedCoordinator) updateWorkerStatus(status, currentTask string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if worker, ok := dc.workers[dc.workerID]; ok {
		worker.Status = status
		worker.CurrentTask = currentTask
		if status == "idle" {
			worker.TasksCompleted++
		}
	}
}

// sendHeartbeat 发送心跳
func (dc *DistributedCoordinator) sendHeartbeat() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if worker, ok := dc.workers[dc.workerID]; ok {
		worker.LastHeartbeat = time.Now()
	}
}

// checkWorkerHealth 检查工作节点健康状态
func (dc *DistributedCoordinator) checkWorkerHealth() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	threshold := time.Now().Add(-3 * dc.config.HeartbeatInterval)

	for id, worker := range dc.workers {
		if worker.LastHeartbeat.Before(threshold) {
			worker.Status = "offline"
			log.Logger.Warnf("Worker %s marked as offline", id)
		}
	}
}

// GetWorkers 获取所有工作节点
func (dc *DistributedCoordinator) GetWorkers() []*WorkerInfo {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	workers := make([]*WorkerInfo, 0, len(dc.workers))
	for _, w := range dc.workers {
		workers = append(workers, w)
	}
	return workers
}

// GetQueueStats 获取队列统计
func (dc *DistributedCoordinator) GetQueueStats() (int64, error) {
	return dc.queue.Len()
}

// 辅助函数
func generateWorkerID() string {
	return "worker_" + time.Now().Format("20060102150405") + "_" + randomString(6)
}

func generateTaskID() string {
	return "task_" + time.Now().Format("20060102150405") + "_" + randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}

// ExportResults 导出结果为JSON
func (dc *DistributedCoordinator) ExportResults(target string) ([]byte, error) {
	results, err := dc.resultStore.List(ResultFilter{Target: target})
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(results, "", "  ")
}
