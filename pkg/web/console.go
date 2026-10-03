package web

import (
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"argo/pkg/engine"
	"argo/pkg/log"
	"argo/pkg/utils"

	"github.com/go-rod/rod"
)

// 内置 Web 控制台 v2：项目制（创建项目/多目标顺序爬取/结果持久化/导出）+
// 左侧 URL 实时流 + 右侧浏览器画面直播（MJPEG，能看到蜘蛛动画）。
//
// 依赖方向：web → engine（无环）。默认只监听 127.0.0.1：本机工具，无鉴权。

//go:embed console.html
var consoleHTML embed.FS

// TaskOptions 任务参数（表单可调的子集）
type TaskOptions struct {
	Target   string `json:"target"`
	MaxDepth int    `json:"maxdepth"`
	TabCount int    `json:"tabcount"`
	Engine   string `json:"engine"`
}

// Console 控制台实例
type Console struct {
	store   *projectStore
	manager *runManager
	hub     *streamHub

	pageMu     sync.Mutex
	current    *rod.Page
	pending    *rod.Page
	lastSwitch time.Time
}

// streamStickyHold 同一页面最短跟随时长：并发 tab 频繁开关时画面如果
// 跟着最新 tab 疯狂翻转，蜘蛛（注入后才开始动画）根本看不清——
// 粘住一个页面至少看这么久，当前页失效才立即切换。
const streamStickyHold = 4 * time.Second

// RegisterPage 引擎每开一个 tab 调用（OnTabOpen 钩子注入）。
// 只登记为候选，是否切换由 currentPage 按粘性策略决定。
func (c *Console) RegisterPage(page *rod.Page) {
	c.pageMu.Lock()
	c.pending = page
	c.pageMu.Unlock()
}

func (c *Console) currentPage() *rod.Page {
	c.pageMu.Lock()
	defer c.pageMu.Unlock()
	now := time.Now()
	if c.current == nil && c.pending != nil {
		c.current = c.pending
		c.lastSwitch = now
		return c.current
	}
	if c.pending != nil && c.pending != c.current && now.Sub(c.lastSwitch) >= streamStickyHold {
		c.current = c.pending
		c.lastSwitch = now
	}
	return c.current
}

func (c *Console) dropPage(p *rod.Page) {
	c.pageMu.Lock()
	if c.current == p {
		c.current = nil
		c.lastSwitch = time.Now()
	}
	if c.pending == p {
		c.pending = nil
	}
	c.pageMu.Unlock()
}

// Start 启动控制台 HTTP 服务（阻塞）。
func Start(addr string) error {
	c := &Console{}
	c.store = newProjectStore(path.Join(utils.GetCurrentDirectory(), "webdata"))
	c.manager = newRunManager(c.store)
	engine.OnTabOpen = c.RegisterPage

	mux := http.NewServeMux()
	mux.HandleFunc("/", c.handleIndex)
	mux.HandleFunc("/api/projects", c.handleProjects)
	mux.HandleFunc("/api/projects/", c.handleProjectItem)
	mux.HandleFunc("/api/results", c.handleResults)
	mux.HandleFunc("/api/stream", c.handleStream)

	c.hub = newStreamHub(c)
	go c.watchStreamPage()

	log.Logger.Infof("[    web   ] 控制台: http://%s/ (仅本机使用，勿暴露公网)", addr)
	server := &http.Server{Addr: addr, Handler: mux}
	return server.ListenAndServe()
}

// watchStreamPage 跟随当前页变化切换 screencast 目标，并回收死页面。
func (c *Console) watchStreamPage() {
	for {
		time.Sleep(500 * time.Millisecond)
		page := c.currentPage()
		if page == nil {
			continue
		}
		c.hub.ensurePage(page)
	}
}

func (c *Console) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := consoleHTML.ReadFile("console.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// handleProjects GET 列表 / POST 创建
func (c *Console) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c.store.listProjects())
	case http.MethodPost:
		var req struct {
			Name    string      `json:"name"`
			Targets []string    `json:"targets"`
			Params  TaskOptions `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), 400)
			return
		}
		var targets []string
		for _, t := range req.Targets {
			if s := strings.TrimSpace(t); s != "" {
				targets = append(targets, s)
			}
		}
		if req.Name == "" || len(targets) == 0 {
			http.Error(w, "name 与 targets 必填", 400)
			return
		}
		p, err := c.store.create(req.Name, targets, req.Params)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(p)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleProjectItem /api/projects/{id}[/{action}]
func (c *Console) handleProjectItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	id, action := parts[2], ""
	if len(parts) >= 4 {
		action = parts[3]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		p := c.store.get(id)
		if p == nil {
			http.NotFound(w, r)
			return
		}
		results := c.store.loadResults(id)
		activeProject, activeTarget := c.manager.current()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"project":       p,
			"results":       results,
			"live":          activeProject == id,
			"active_target": activeTarget,
		})

	case action == "" && r.Method == http.MethodDelete:
		if active, _ := c.manager.current(); active == id {
			http.Error(w, "项目运行中，先停止", 409)
			return
		}
		if err := c.store.remove(id); err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})

	case action == "start" && r.Method == http.MethodPost:
		if err := c.manager.start(id); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})

	case action == "stop" && r.Method == http.MethodPost:
		if active, _ := c.manager.current(); active != id {
			http.Error(w, "该项目未在运行", 409)
			return
		}
		c.manager.stop()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})

	case action == "export" && r.Method == http.MethodGet:
		c.handleExport(w, r, id)

	default:
		http.Error(w, "not found", 404)
	}
}

// handleExport 导出项目结果：?format=txt|json|csv
func (c *Console) handleExport(w http.ResponseWriter, r *http.Request, id string) {
	p := c.store.get(id)
	if p == nil {
		http.NotFound(w, r)
		return
	}
	results := c.store.loadResults(id)
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "txt"
	}
	filename := fmt.Sprintf("%s-results.%s", sanitizeFilename(p.Name), format)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"method", "url", "status"})
		for _, item := range results {
			_ = cw.Write([]string{item.Method, item.URL, fmt.Sprint(item.Status)})
		}
		cw.Flush()
	default: // txt
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, item := range results {
			fmt.Fprintf(w, "[%s]%s\n", item.Method, item.URL)
		}
	}
}

func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			return '_'
		}
		return r
	}, name)
}

// handleResults SSE 增量推送结果（500ms 轮询，按代数重置游标）。
// 事件带 project_id（当前运行项目），前端过滤非当前项目的事件。
func (c *Console) handleResults(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	cursor := 0
	lastGen := int64(-1)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			gen := engine.ResultGen()
			if lastGen == -1 {
				lastGen = gen
			} else if gen != lastGen {
				lastGen = gen
				cursor = 0
				activeProject, _ := c.manager.current()
				fmt.Fprintf(w, "event: reset\ndata: {\"project_id\":%q}\n\n", activeProject)
				flusher.Flush()
				continue
			}
			results := engine.SnapshotResult()
			if cursor > len(results) {
				cursor = len(results)
			}
			if len(results) > cursor {
				items := results[cursor:]
				payload, err := json.Marshal(items)
				if err == nil {
					activeProject, _ := c.manager.current()
					meta, _ := json.Marshal(map[string]string{"project_id": activeProject})
					fmt.Fprintf(w, "event: items\ndata: {\"meta\":%s,\"items\":%s}\n\n", meta, payload)
					flusher.Flush()
				}
				cursor = len(results)
			}
		}
	}
}

// streamFrameMinInterval 帧节流下限（~15fps 上限）：浏览器动画期间合成器
// 可能产帧更快，节流保证带宽与解码压力可控
const streamFrameMinInterval = 66 * time.Millisecond

// streamDeadTimeout 这么久没有新帧视为页面已死（关闭/导航），切换跟随目标
const streamDeadTimeout = 3 * time.Second

// handleStream MJPEG：订阅 screencast 广播（见 streamHub）。
func (c *Console) handleStream(w http.ResponseWriter, r *http.Request) {
	boundary := "argoframe"
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	frames, unsub := c.hub.subscribe()
	defer unsub()
	for {
		select {
		case <-r.Context().Done():
			return
		case img := <-frames:
			fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(img))
			if _, err := w.Write(img); err != nil {
				return
			}
			fmt.Fprint(w, "\r\n")
			flusher.Flush()
		}
	}
}
