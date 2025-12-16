package engine

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/utils"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
)

// 输出结果

type HtmlData struct {
	HostName   string
	DateTime   string
	ResultList []*PendingUrl
	Count      int
}

type ResultSink interface {
	Name() string
	Save(ei *EngineInfo, path string) error
}

func (ei *EngineInfo) InitResultHandler() {
	ei.ResultList = make([]*PendingUrl, 0)
	ei.ResultQueue = make(chan *PendingUrl, 1024)
	ei.ResultSinks = make(map[string]ResultSink)
	ei.registerDefaultSinks()
	ei.resultWg.Add(1)
	go ei.resultHandlerWork()
}

func (ei *EngineInfo) registerDefaultSinks() {
	sinks := []ResultSink{
		&jsonSink{},
		&textSink{},
		&xlsxSink{},
		&htmlSink{},
		&jsonlSink{},
	}
	for _, sink := range sinks {
		ei.ResultSinks[sink.Name()] = sink
	}
	if conf.GlobalConfig.ResultConf.MQ.Type != "" {
		ei.ResultSinks["mq"] = newMQSink(conf.GlobalConfig.ResultConf.MQ)
	}
}

func (ei *EngineInfo) pushResult(pu *PendingUrl) {
	if ei.ResultQueue == nil {
		return
	}
	ei.ResultQueue <- pu
}

func (ei *EngineInfo) resultHandlerWork() {
	defer ei.resultWg.Done()
	for data := range ei.ResultQueue {
		if conf.GlobalConfig.Quiet {
			jsonData, _ := json.Marshal(data)
			fmt.Println(string(jsonData))
		} else {
			ei.ResultList = append(ei.ResultList, data)
			from := ""
			if data.SourceType != "" {
				from = fmt.Sprintf("[%s]", data.SourceType)
			}
			if data.SourceUrl != "" {
				log.Logger.Infof("[%s]%s %s <- %s", data.Method, from, data.URL, data.SourceUrl)
			} else {
				log.Logger.Infof("[%s]%s %s", data.Method, from, data.URL)
			}
		}
	}
}

func (ei *EngineInfo) closeResultQueue() {
	if ei == nil {
		return
	}
	ei.resultCloseOnce.Do(func() {
		if ei.ResultQueue != nil {
			close(ei.ResultQueue)
		}
	})
}

func (ei *EngineInfo) writeResult(name string, data []byte) {
	resultFile, err := os.Create(name)
	if err != nil {
		log.Logger.Errorf(" %s file creation error: %s", name, err)
		return
	}
	defer resultFile.Close()

	_, err = resultFile.Write(data)
	if err != nil {
		log.Logger.Errorf("%s file write error: %s", name, err)
		return
	}
}

func (ei *EngineInfo) SaveResult() {
	// 先关闭结果管道并等待消费完成，确保 ResultList 已完全汇总后再落盘。
	ei.closeResultQueue()
	ei.resultWg.Wait()

	log.Logger.Infof("[tab  count] %d", ei.TabCount)
	if len(ei.ResultList) < 2 {
		log.Logger.Errorf("No content crawled, you can contact the developer to recar target: %s", ei.HostName)
		ei.logMetrics()
		return
	}
	log.Logger.Infof("[  result  ] %d", len(ei.ResultList))
	var ResultOutPutDir string
	if conf.GlobalConfig.ResultConf.OutputDir == "" {
		ResultOutPutDir = path.Join(utils.GetCurrentDirectory(), "result", ei.HostName)
	} else {
		ResultOutPutDir = conf.GlobalConfig.ResultConf.OutputDir
	}
	if !utils.IsExist(ResultOutPutDir) {
		err := os.MkdirAll(ResultOutPutDir, os.ModePerm)
		if err != nil {
			log.Logger.Errorf("create result dir %s error: %s", ResultOutPutDir, err)
		}
	}
	saveName := conf.GlobalConfig.ResultConf.Name
	if saveName == "" {
		saveName = ei.HostName
	}
	formatList := strings.Split(conf.GlobalConfig.ResultConf.Format, ",")
	for _, format := range formatList {
		format = strings.TrimSpace(format)
		if format == "" {
			continue
		}
		sink, ok := ei.ResultSinks[format]
		if !ok {
			log.Logger.Errorf("format not found: %s", format)
			continue
		}
		if format == "html" {
			ei.ResultHtmlData = &HtmlData{
				HostName:   ei.HostName,
				DateTime:   utils.GetCurrentTime(),
				ResultList: ei.ResultList,
				Count:      len(ei.ResultList),
			}
		}
		fileName := saveName + "." + format
		filePath := path.Join(ResultOutPutDir, fileName)
		if err := sink.Save(ei, filePath); err != nil {
			log.Logger.Errorf("save %s err: %s", format, err)
			continue
		}
		log.Logger.Infof("[   save   ] %s", filePath)
	}
	if conf.GlobalConfig.SeedOutput != "" {
		writeSeeds(conf.GlobalConfig.SeedOutput, ei.ResultList)
	}
	ei.logMetrics()
}

func writeSeeds(path string, results []*PendingUrl) {
	if len(results) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(results))
	lines := make([]string, 0, len(results))
	for _, item := range results {
		if item == nil || item.URL == "" {
			continue
		}
		url := item.URL
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		lines = append(lines, url)
	}
	if len(lines) == 0 {
		return
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		log.Logger.Warnf("write seed file err: %s", err)
	} else {
		log.Logger.Infof("[ seed save ] %s", path)
	}
}

func (ei *EngineInfo) logMetrics() {
	log.Logger.Infof("[ metrics ] pages=%d dropped=%d tab_timeout=%d", ei.PagesProcessed, ei.UrlsDropped, ei.TabsTimeout)
	ei.timeoutReasonMu.Lock()
	if len(ei.TimeoutReasons) > 0 {
		for stage, count := range ei.TimeoutReasons {
			log.Logger.Infof("[ timeout reason ] stage=%s count=%d", stage, count)
		}
	}
	ei.timeoutReasonMu.Unlock()
}
