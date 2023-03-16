package engine

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/utils"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/template"

	"github.com/tealeg/xlsx"
)

// 输出结果

type HtmlData struct {
	HostName   string
	DateTime   string
	ResultList []*PendingUrl
	Count      int
}

type FormatOutputFunc func(name string)

var ResultHtmlData *HtmlData
var ResultList []*PendingUrl
var ResultQueue chan *PendingUrl
var FormatMap map[string]FormatOutputFunc

func InitResultHandler() {
	ResultList = make([]*PendingUrl, 0)
	ResultQueue = make(chan *PendingUrl)
	FormatMap = make(map[string]FormatOutputFunc)
	go resultHandlerWork()
	FormatMap["json"] = writeResultToJson
	FormatMap["txt"] = writeResultToText
	FormatMap["xlsx"] = writeResultToXlsx
	FormatMap["html"] = writeResultToHtml
}

func pushResult(pu *PendingUrl) {
	ResultQueue <- pu
}

func resultHandlerWork() {
	for data := range ResultQueue {
		ResultList = append(ResultList, data)
		log.Logger.Infof("[%s] %s", data.Method, data.URL)
	}
}

func writeResult(name string, data []byte) {
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
func writeResultToJson(name string) {
	jsonData, err := json.MarshalIndent(ResultList, "", "    ")
	if err != nil {
		log.Logger.Errorf("save result err: %s", err)
		return
	}
	writeResult(name, jsonData)
}

func writeResultToText(name string) {
	txtDate := ""
	urlCount := 0
	for _, r := range ResultList {
		urlCount += 1
		txtDate += fmt.Sprintf("[%s]%s\n", r.Method, r.URL)
	}
	writeResult(name, []byte(txtDate))
}

func writeResultToXlsx(name string) {
	xlsxFile := xlsx.NewFile()
	sheet, err := xlsxFile.AddSheet("Argo result")
	if err != nil {
		log.Logger.Errorf("writeResultToXlsx err: %s", err)
	}
	titles := []string{"method", "url", "data", "status"}
	row := sheet.AddRow()

	var cell *xlsx.Cell
	for _, title := range titles {
		cell = row.AddCell()
		cell.Value = title
	}
	// 设置宽度
	sheet.SetColWidth(0, 0, 5)
	sheet.SetColWidth(1, 1, 80)
	sheet.SetColWidth(2, 2, 80)
	sheet.SetColWidth(3, 3, 5)
	for _, data := range ResultList {
		values := []string{
			data.Method,
			data.URL,
			data.Data,
			strconv.Itoa(data.Status),
		}

		row = sheet.AddRow()

		for _, value := range values {
			cell = row.AddCell()
			cell.Value = value
		}
	}
	err = xlsxFile.Save(name)

}

func writeResultToHtml(name string) {
	t, err := template.New("result").Parse(ResultHtmlTemplate)
	if err != nil {
		log.Logger.Errorf("writeResultToHtml err: %s", err)
	}
	resultFile, err := os.Create(name)
	if err != nil {
		log.Logger.Errorf(" %s file creation error: %s", name, err)
		return
	}
	defer resultFile.Close()
	err = t.Execute(resultFile, ResultHtmlData)
	if err != nil {
		log.Logger.Errorf(" %s file creation error: %s", name, err)
		return
	}
}

func SaveResult(target string) {
	log.Logger.Infof("[tab  count] %d", newTabCount)
	log.Logger.Infof("[  result  ] %d", len(ResultList))

	u, _ := url.Parse(target)
	saveName := conf.GlobalConfig.ResultConf.Name
	if saveName == "" {
		saveName = u.Hostname()
	}
	formatList := strings.Split(conf.GlobalConfig.ResultConf.Format, ",")
	for _, format := range formatList {
		if format == "html" {
			ResultHtmlData = &HtmlData{
				HostName:   u.Hostname(),
				DateTime:   utils.GetCurrentTime(),
				ResultList: ResultList,
				Count:      len(ResultList),
			}
		}
		if _, ok := FormatMap[format]; ok {
			fileName := saveName + "." + format
			FormatMap[format](fileName)
			log.Logger.Infof("[   save   ] %s", fileName)
		} else {
			log.Logger.Errorf("format out found: %s", format)
		}
	}

}
