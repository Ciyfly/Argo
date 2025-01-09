package engine

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/utils"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
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

func pushResult(pu *PendingUrl) {
	ResultQueue <- pu
}

func resultHandlerWork(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-ResultQueue:
			if !ok {
				return
			}
			if conf.GlobalConfig.Quiet {
				jsonData, _ := json.Marshal(data)
				fmt.Println(string(jsonData))
			} else {
				ResultList = append(ResultList, data)
				log.Logger.Infof("[%s] %s", data.Method, data.URL)
			}
		}
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

func (ei *EngineInfo) SaveResult() {
	log.Logger.Infof("[tab  count] %d", ei.TabCount)
	if len(ResultList) < 2 {
		log.Logger.Errorf("No content crawled, you can contact the developer to recar target: %s", ei.HostName)
		return
	}
	log.Logger.Infof("[  result  ] %d", len(ResultList))

	// 如果指定了MergedOutput，优先使用它作为输出文件
	if conf.GlobalConfig.ResultConf.MergedOutput != "" {
		// 检查MergedOutput是否包含文件扩展名
		ext := path.Ext(conf.GlobalConfig.ResultConf.MergedOutput)
		if ext != "" {
			// 如果指定了扩展名，只保存对应格式
			format := strings.TrimPrefix(ext, ".")
			if _, ok := FormatMap[format]; ok {
				err := appendToFile(conf.GlobalConfig.ResultConf.MergedOutput, format, ei)
				if err != nil {
					log.Logger.Errorf("Failed to save merged result to %s: %v", conf.GlobalConfig.ResultConf.MergedOutput, err)
				}
				return
			} else {
				log.Logger.Errorf("Unsupported format in MergedOutput filename: %s", format)
			}
		} else {
			// 如果没有指定扩展名，使用format参数中指定的所有格式
			formatList := strings.Split(conf.GlobalConfig.ResultConf.Format, ",")
			baseDir := path.Dir(conf.GlobalConfig.ResultConf.MergedOutput)
			baseName := path.Base(conf.GlobalConfig.ResultConf.MergedOutput)

			for _, format := range formatList {
				if format == "html" {
					ResultHtmlData = &HtmlData{
						HostName:   ei.HostName,
						DateTime:   utils.GetCurrentTime(),
						ResultList: ResultList,
						Count:      len(ResultList),
					}
				}
				if _, ok := FormatMap[format]; ok {
					fileName := baseName + "." + format
					filePath := path.Join(baseDir, fileName)
					err := appendToFile(filePath, format, ei)
					if err != nil {
						log.Logger.Errorf("Failed to save merged result to %s: %v", filePath, err)
					}
				} else {
					log.Logger.Errorf("Format not found: %s", format)
				}
			}
			return
		}
	}

	// 如果没有指定MergedOutput，使用原来的保存逻辑
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
		if format == "html" {
			ResultHtmlData = &HtmlData{
				HostName:   ei.HostName,
				DateTime:   utils.GetCurrentTime(),
				ResultList: ResultList,
				Count:      len(ResultList),
			}
		}
		if _, ok := FormatMap[format]; ok {
			fileName := saveName + "." + format
			filePath := path.Join(ResultOutPutDir, fileName)
			FormatMap[format](filePath)
			log.Logger.Infof("[   save   ] %s", filePath)
		} else {
			log.Logger.Errorf("format not found: %s", format)
		}
	}
}

// appendToFile 根据不同格式追加内容到文件
func appendToFile(filePath string, format string, ei *EngineInfo) error {
	switch format {
	case "txt":
		return appendTxtResult(filePath, ResultList)
	case "json":
		return appendJsonResult(filePath, ResultList)
	case "xlsx":
		return appendXlsxResult(filePath, ResultList)
	case "html":
		return appendHtmlResult(filePath, ResultHtmlData)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

// appendTxtResult 追加文本格式结果
func appendTxtResult(filePath string, results []*PendingUrl) error {
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, r := range results {
		_, err = fmt.Fprintf(f, "[%s]%s\n", r.Method, r.URL)
		if err != nil {
			return err
		}
	}
	return nil
}

// appendJsonResult 追加JSON格式结果
func appendJsonResult(filePath string, results []*PendingUrl) error {
	var existingData []*PendingUrl

	// 读取现有文件
	if utils.IsExist(filePath) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		if len(data) > 0 {
			err = json.Unmarshal(data, &existingData)
			if err != nil {
				return err
			}
		}
	}

	// 合并数据
	existingData = append(existingData, results...)

	// 写入文件
	data, err := json.MarshalIndent(existingData, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}

// appendXlsxResult 追加XLSX格式结果
func appendXlsxResult(filePath string, results []*PendingUrl) error {
	var xlsxFile *xlsx.File
	if utils.IsExist(filePath) {
		// 如果文件存在，打开它
		var err error
		xlsxFile, err = xlsx.OpenFile(filePath)
		if err != nil {
			return err
		}
	} else {
		// 如果文件不存在，创建新文件
		xlsxFile = xlsx.NewFile()
		sheet, err := xlsxFile.AddSheet("Argo result")
		if err != nil {
			return fmt.Errorf("failed to add sheet: %v", err)
		}

		// 添加标题行
		titles := []string{"method", "url", "data", "status"}
		row := sheet.AddRow()
		for _, title := range titles {
			cell := row.AddCell()
			cell.Value = title
		}

		// 设置列宽
		sheet.SetColWidth(0, 0, 5)  // method
		sheet.SetColWidth(1, 1, 80) // url
		sheet.SetColWidth(2, 2, 80) // data
		sheet.SetColWidth(3, 3, 5)  // status
	}

	// 获取第一个sheet（如果是新文件，就是我们刚创建的sheet）
	sheet := xlsxFile.Sheets[0]

	// 追加数据
	for _, data := range results {
		values := []string{
			data.Method,
			data.URL,
			data.Data,
			strconv.Itoa(data.Status),
		}

		row := sheet.AddRow()
		for _, value := range values {
			cell := row.AddCell()
			cell.Value = value
		}
	}

	// 保存文件
	return xlsxFile.Save(filePath)
}

// appendHtmlResult 追加HTML格式结果
func appendHtmlResult(filePath string, htmlData *HtmlData) error {
	var existingData *HtmlData

	if utils.IsExist(filePath) {
		// 如果文件已存在，需要先读取现有数据
		// 由于HTML是模板格式，我们需要解析现有文件来提取数据
		// 这里采用一个简单的方案：创建新的合并数据
		existingData = &HtmlData{
			HostName:   htmlData.HostName,
			DateTime:   utils.GetCurrentTime(),
			ResultList: make([]*PendingUrl, 0),
			Count:      0,
		}
	} else {
		existingData = &HtmlData{
			HostName:   htmlData.HostName,
			DateTime:   utils.GetCurrentTime(),
			ResultList: make([]*PendingUrl, 0),
			Count:      0,
		}
	}

	// 合并数据
	existingData.ResultList = append(existingData.ResultList, htmlData.ResultList...)
	existingData.Count = len(existingData.ResultList)

	// 使用模板重新生成完整的HTML文件
	t, err := template.New("result").Parse(ResultHtmlTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse template: %v", err)
	}

	// 创建或覆盖文件
	resultFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer resultFile.Close()

	// 执行模板，写入数据
	err = t.Execute(resultFile, existingData)
	if err != nil {
		return fmt.Errorf("failed to execute template: %v", err)
	}

	return nil
}

// FormatMap的初始化需要修改为使用新的追加模式函数
func InitResultHandler(ctx context.Context) {
	ResultList = make([]*PendingUrl, 0)
	ResultQueue = make(chan *PendingUrl)
	FormatMap = make(map[string]FormatOutputFunc)
	go resultHandlerWork(ctx)

	// 如果使用MergedOutput，使用追加模式的处理函数
	if conf.GlobalConfig.ResultConf.MergedOutput != "" {
		FormatMap["json"] = func(name string) {
			err := appendJsonResult(name, ResultList)
			if err != nil {
				log.Logger.Errorf("Failed to append json result: %v", err)
			}
		}
		FormatMap["txt"] = func(name string) {
			err := appendTxtResult(name, ResultList)
			if err != nil {
				log.Logger.Errorf("Failed to append txt result: %v", err)
			}
		}
		FormatMap["xlsx"] = func(name string) {
			err := appendXlsxResult(name, ResultList)
			if err != nil {
				log.Logger.Errorf("Failed to append xlsx result: %v", err)
			}
		}
		FormatMap["html"] = func(name string) {
			err := appendHtmlResult(name, ResultHtmlData)
			if err != nil {
				log.Logger.Errorf("Failed to append html result: %v", err)
			}
		}
	} else {
		// 使用原来的写入模式
		FormatMap["json"] = writeResultToJson
		FormatMap["txt"] = writeResultToText
		FormatMap["xlsx"] = writeResultToXlsx
		FormatMap["html"] = writeResultToHtml
	}
}
