package web

import (
	"argo/pkg/conf"
	"argo/pkg/engine"
	"argo/pkg/log"
	"argo/pkg/utils"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sync"
	"time"
)

// 项目存储与运行管理。
//
// 全局单活跃爬取（engine 的包级全局状态约束）：同一时刻只有一个项目的
// 一个目标在跑；项目内多个目标顺序执行，每个目标完成后结果并入项目存储。

// TargetState 单个目标的执行状态
type TargetState struct {
	URL         string `json:"url"`
	Status      string `json:"status"` // pending / running / done / stopped / failed
	ResultCount int    `json:"result_count"`
	Error       string `json:"error,omitempty"`
}

// Project 项目元数据
type Project struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Targets   []TargetState `json:"targets"`
	Params    TaskOptions   `json:"params"`
	CreatedAt string        `json:"created_at"`
	Status    string        `json:"status"` // idle / running / done / stopped
}

const (
	targetPending = "pending"
	targetRunning = "running"
	targetDone    = "done"
	targetStopped = "stopped"
	targetFailed  = "failed"
)

// projectStore 项目存储（webdata/ 目录，JSON 文件）
type projectStore struct {
	mu   sync.RWMutex
	dir  string
	list []*Project
}

func newProjectStore(dir string) *projectStore {
	s := &projectStore{dir: dir}
	s.load()
	return s
}

func (s *projectStore) indexFile() string { return path.Join(s.dir, "projects.json") }
func (s *projectStore) resultsFile(id string) string {
	return path.Join(s.dir, id+".results.json")
}

func (s *projectStore) load() {
	data, err := os.ReadFile(s.indexFile())
	if err != nil {
		return
	}
	var list []*Project
	if json.Unmarshal(data, &list) == nil {
		s.list = list
	}
}

// saveIndex 原子写项目索引。
func (s *projectStore) saveIndex() error {
	if err := os.MkdirAll(s.dir, os.ModePerm); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.indexFile() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.indexFile())
}

func (s *projectStore) listProjects() []*Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Project, len(s.list))
	copy(out, s.list)
	return out
}

func (s *projectStore) get(id string) *Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.list {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (s *projectStore) create(name string, targets []string, params TaskOptions) (*Project, error) {
	if name == "" || len(targets) == 0 {
		return nil, fmt.Errorf("name 与 targets 必填")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &Project{
		ID:        fmt.Sprintf("p_%s", utils.GetMD5(name + fmt.Sprint(time.Now().UnixNano()))[:12]),
		Name:      name,
		Params:    params,
		CreatedAt: utils.GetCurrentTime(),
		Status:    "idle",
	}
	for _, t := range targets {
		p.Targets = append(p.Targets, TargetState{URL: t, Status: targetPending})
	}
	s.list = append(s.list, p)
	if err := s.saveIndex(); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *projectStore) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.list {
		if p.ID == id {
			s.list = append(s.list[:i], s.list[i+1:]...)
			_ = os.Remove(s.resultsFile(id))
			return s.saveIndex()
		}
	}
	return fmt.Errorf("project not found")
}

// mutate 在锁内修改项目字段并落盘。
func (s *projectStore) mutate(id string, fn func(p *Project)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.list {
		if p.ID == id {
			fn(p)
			return s.saveIndex()
		}
	}
	return fmt.Errorf("project not found")
}

// appendResults 把一批结果追加进项目存储文件。
func (s *projectStore) appendResults(id string, items []*engine.PendingUrl) error {
	if len(items) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing []*engine.PendingUrl
	if data, err := os.ReadFile(s.resultsFile(id)); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	existing = append(existing, items...)
	data, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, os.ModePerm); err != nil {
		return err
	}
	tmp := s.resultsFile(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.resultsFile(id))
}

// loadResults 读项目累积结果。
func (s *projectStore) loadResults(id string) []*engine.PendingUrl {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var items []*engine.PendingUrl
	if data, err := os.ReadFile(s.resultsFile(id)); err == nil {
		_ = json.Unmarshal(data, &items)
	}
	return items
}

// ---- runManager：全局单活跃爬取 + 项目内目标顺序执行 ----

type runManager struct {
	store *projectStore

	mu            sync.Mutex
	activeProject string
	activeTarget  int
	stopRequested bool
}

func newRunManager(store *projectStore) *runManager {
	return &runManager{store: store}
}

// current 返回当前运行的项目 ID 与目标下标（未运行时空）。
func (m *runManager) current() (string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeProject, m.activeTarget
}

// start 启动项目：从第一个未完成的目标顺序爬取。
func (m *runManager) start(projectID string) error {
	m.mu.Lock()
	if m.activeProject != "" {
		active := m.activeProject
		m.mu.Unlock()
		return fmt.Errorf("项目 %s 正在运行，先停止", active)
	}
	p := m.store.get(projectID)
	if p == nil {
		m.mu.Unlock()
		return fmt.Errorf("project not found")
	}
	m.activeProject = projectID
	m.stopRequested = false
	m.mu.Unlock()

	go m.runLoop(projectID)
	return nil
}

// stop 停止当前爬取并中止项目的后续目标。
func (m *runManager) stop() {
	m.mu.Lock()
	m.stopRequested = true
	m.mu.Unlock()
	engine.StopCurrent()
}

// runLoop 顺序执行项目目标。每个目标的结果在完成后并入项目存储。
func (m *runManager) runLoop(projectID string) {
	defer func() {
		m.mu.Lock()
		m.activeProject = ""
		m.activeTarget = -1
		m.mu.Unlock()
	}()

	for idx := 0; idx < len(m.store.get(projectID).Targets); idx++ {
		m.mu.Lock()
		stopped := m.stopRequested
		m.activeTarget = idx
		m.mu.Unlock()
		if stopped {
			m.markTarget(projectID, idx, targetStopped, 0, "")
			continue
		}
		target := m.store.get(projectID).Targets[idx]
		if target.Status == targetDone || target.Status == targetStopped {
			continue
		}

		_ = m.store.mutate(projectID, func(p *Project) {
			p.Status = "running"
			p.Targets[idx].Status = targetRunning
			p.Targets[idx].Error = ""
		})
		log.Logger.Infof("[   web   ] 项目 %s 目标 %d/%d: %s", projectID, idx+1, len(m.store.get(projectID).Targets), target.URL)

		// 表单参数写回全局配置（engine.Run 读 GlobalConfig）
		opts := m.store.get(projectID).Params
		conf.GlobalConfig.BrowserConf.MaxDepth = opts.MaxDepth
		if opts.TabCount > 0 {
			conf.GlobalConfig.BrowserConf.TabCount = opts.TabCount
		}
		if opts.Engine != "" {
			conf.GlobalConfig.BrowserConf.Engine = opts.Engine
		}
		// web 任务默认不存 req/resp base64：项目结果文件会膨胀到不可用
		conf.GlobalConfig.NoReqRspStr = true

		done := engine.RunAsync(target.URL)
		<-done

		// engine 每目标 ResetResult，快照即本目标增量
		results := engine.SnapshotResult()
		if err := m.store.appendResults(projectID, results); err != nil {
			log.Logger.Errorf("[   web   ] 项目结果写盘 err: %s", err)
		}
		m.mu.Lock()
		stopped = m.stopRequested
		m.mu.Unlock()
		status := targetDone
		errStr := ""
		if stopped {
			status = targetStopped
		} else if len(results) == 0 {
			// 零结果：目标大概率不可达（连不上/一直超时）
			status = targetFailed
			errStr = "未获得任何结果（目标可能不可达）"
		}
		m.markTarget(projectID, idx, status, len(results), errStr)
	}
	// 项目级状态收尾
	_ = m.store.mutate(projectID, func(p *Project) {
		allDone := true
		anyStopped := false
		anyFailed := false
		for _, t := range p.Targets {
			if t.Status != targetDone && t.Status != targetFailed {
				allDone = false
			}
			if t.Status == targetStopped {
				anyStopped = true
			}
			if t.Status == targetFailed {
				anyFailed = true
			}
		}
		switch {
		case anyStopped:
			p.Status = "stopped"
		case allDone && anyFailed:
			p.Status = "done" // 有失败目标但全部跑完
		case allDone:
			p.Status = "done"
		default:
			p.Status = "idle"
		}
	})
}

// markTarget 记录目标终态。resultCount 是本目标的增量结果数
// （engine 每目标清空结果列表，快照即增量）。
func (m *runManager) markTarget(projectID string, idx int, status string, resultCount int, errStr string) {
	_ = m.store.mutate(projectID, func(p *Project) {
		if idx >= 0 && idx < len(p.Targets) {
			p.Targets[idx].Status = status
			p.Targets[idx].Error = errStr
			p.Targets[idx].ResultCount = resultCount
		}
	})
}
