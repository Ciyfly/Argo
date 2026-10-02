package engine

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/utils"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path"
	"time"
)

// 断点续爬：把「已访问去重键 + 待处理 URL」持久化为状态文件，
// --resume 恢复后不重复访问已完成页面。
//
// 登录态无法持久化（浏览器会话随进程销毁）——恢复后需用
// --cookie / 自动登录 / --waitlogin 重建会话，README 已写明。

// stateSaveInterval 定期保存间隔
const stateSaveInterval = 10 * time.Second

// crawlState 状态文件结构
type crawlState struct {
	Target  string     `json:"target"`
	SavedAt string     `json:"saved_at"`
	Visited []string   `json:"visited"`
	Pending []*UrlInfo `json:"pending"`
}

// stateFilePath 状态文件路径：与结果文件同目录同名（<save>.state.json）。
func stateFilePath() string {
	var outputDir string
	if conf.GlobalConfig.ResultConf.OutputDir != "" {
		outputDir = conf.GlobalConfig.ResultConf.OutputDir
	} else if conf.GlobalConfig.ResultConf.MergedOutput != "" {
		outputDir = path.Dir(conf.GlobalConfig.ResultConf.MergedOutput)
	} else {
		outputDir = path.Join(utils.GetCurrentDirectory(), "result", hostnameOfTarget(lastTarget))
	}
	saveName := conf.GlobalConfig.ResultConf.Name
	if saveName == "" {
		saveName = hostnameOfTarget(lastTarget)
	}
	return path.Join(outputDir, saveName+".state.json")
}

// hostnameOfTarget 从目标 URL 提取主机名（与结果目录命名同源）。
func hostnameOfTarget(target string) string {
	if u, err := url.Parse(target); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "target"
}

// lastTarget 当前目标（Run 时记录，多目标串行时逐个覆盖）
var lastTarget string

// startStateSaver 定期保存断点状态（后台协程）。
func startStateSaver(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(stateSaveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				SaveCrawlState()
			}
		}
	}()
}

// SaveCrawlState 原子写入断点状态文件（tmp + rename，进程被杀也不会留半截文件）。
func SaveCrawlState() {
	state := crawlState{
		Target:  lastTarget,
		SavedAt: utils.GetCurrentTime(),
		Visited: VisitedKeysSnapshot(),
		Pending: SnapshotPendingShadow(),
	}
	data, err := json.Marshal(state)
	if err != nil {
		log.Logger.Debugf("save crawl state marshal err: %s", err)
		return
	}
	filePath := stateFilePath()
	if err := os.MkdirAll(path.Dir(filePath), os.ModePerm); err != nil {
		log.Logger.Debugf("save crawl state mkdir err: %s", err)
		return
	}
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		log.Logger.Debugf("save crawl state err: %s", err)
		return
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		log.Logger.Debugf("save crawl state rename err: %s", err)
		return
	}
	log.Logger.Debugf("crawl state saved: %s (visited=%d pending=%d)", filePath, len(state.Visited), len(state.Pending))
}

// FlushCrawlStateOnExit 信号退出时的断点保存回调（cmd 注入）。
var FlushCrawlStateOnExit = SaveCrawlState

// LoadCrawlState 加载 --resume 状态文件：校验目标一致后注入已访问键并回填待处理 URL。
// 必须在 InitEngine 之前调用（队列尚未创建）。
func LoadCrawlState(resumePath string, target string) error {
	data, err := os.ReadFile(resumePath)
	if err != nil {
		return err
	}
	var state crawlState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Target != "" && state.Target != target {
		log.Logger.Fatalf("状态文件目标 %s 与当前目标 %s 不一致（去重键按 URL 计算，混用会丢正确性）", state.Target, target)
	}
	ImportVisitedKeys(state.Visited)
	for _, uif := range state.Pending {
		PushUrlQueue(uif)
	}
	log.Logger.Infof("[  resume  ] 恢复断点: visited=%d pending=%d (%s)", len(state.Visited), len(state.Pending), resumePath)
	return nil
}
