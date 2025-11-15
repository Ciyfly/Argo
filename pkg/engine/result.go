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
	ei.ResultQueue = make(chan *PendingUrl)
	ei.ResultSinks = make(map[string]ResultSink)
	ei.registerDefaultSinks()
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
	for data := range ei.ResultQueue {
		if conf.GlobalConfig.Quiet {
			jsonData, _ := json.Marshal(data)
			fmt.Println(string(jsonData))
		} else {
			ei.ResultList = append(ei.ResultList, data)
			log.Logger.Infof("[%s] %s", data.Method, data.URL)
		}
	}
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
	log.Logger.Infof("[tab  count] %d", ei.TabCount)
	if len(ei.ResultList) < 2 {
		log.Logger.Errorf("No content crawled, you can contact the developer to recar target: %s", ei.HostName)
		if ei.ResultQueue != nil {
			close(ei.ResultQueue)
		}
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
	if ei.ResultQueue != nil {
		close(ei.ResultQueue)
	}
	ei.logMetrics()
}

func (ei *EngineInfo) logMetrics() {
	log.Logger.Infof("[ metrics ] pages=%d dropped=%d tab_timeout=%d", ei.PagesProcessed, ei.UrlsDropped, ei.TabsTimeout)
}
