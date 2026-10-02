package engine

import (
	"argo/pkg/conf"
	"argo/pkg/extract"
	"argo/pkg/log"
	"argo/pkg/scope"
	"argo/pkg/utils"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/tealeg/xlsx"
)

// 输出结果

// HtmlData 既是 html 模板的渲染数据，也是传给各格式写文件函数的快照载体。
// 之所以要快照：结果列表会被后台协程持续追加，写文件时不能再直接遍历全局变量。
type HtmlData struct {
	HostName   string
	DateTime   string
	ResultList []*PendingUrl
	Count      int
}

type FormatOutputFunc func(name string, data *HtmlData)

// ResultList 由 resultHandlerWork 协程写入，被主协程（落盘）读取，
// 所有访问都必须经过 resultMu，否则并发下会丢结果甚至 panic。
var (
	resultMu    sync.Mutex
	ResultList  []*PendingUrl
	ResultQueue chan *PendingUrl
	FormatMap   map[string]FormatOutputFunc
)

// 计数用于退出前确认所有已抓到的结果都已落到 ResultList，避免丢尾巴。
var (
	resultPushCount    int64
	resultHandledCount int64
)

// SnapshotResult 返回结果列表的副本，避免调用方遍历时被并发写入影响。
func SnapshotResult() []*PendingUrl {
	resultMu.Lock()
	defer resultMu.Unlock()
	out := make([]*PendingUrl, len(ResultList))
	copy(out, ResultList)
	return out
}

// NewResultSnapshot 生成当前结果的快照，供各格式写文件函数使用。
func (ei *EngineInfo) NewResultSnapshot() *HtmlData {
	results := SnapshotResult()
	return &HtmlData{
		HostName:   ei.HostName,
		DateTime:   utils.GetCurrentTime(),
		ResultList: results,
		Count:      len(results),
	}
}

// resultCount 返回当前已收集的结果条数。
func resultCount() int {
	resultMu.Lock()
	defer resultMu.Unlock()
	return len(ResultList)
}

// ResetResult 清空结果列表与计数器。
//
// fix: 多个目标时 ResultList 是包级变量，第二个目标会接着第一个目标的结果继续追加，
// 所以每个目标开始前必须清空，否则前一个目标的 URL 会被重复写入后一个目标的文件。
func ResetResult() {
	resultMu.Lock()
	ResultList = make([]*PendingUrl, 0)
	resultMu.Unlock()
	atomic.StoreInt64(&resultPushCount, 0)
	atomic.StoreInt64(&resultHandledCount, 0)
	ResetSecrets()
}

// ---- 密钥泄漏结果收集（模式同 ResultList：锁保护 + 快照读取）----

var (
	secretMu       sync.Mutex
	SecretFindings []extract.SecretFinding
	secretSeen     map[string]bool
)

// CollectSecret 收集一条密钥命中，按 规则+打码值+URL 去重。
// 打码值直接进日志与结果文件，完整明文不存在于任何输出。
func CollectSecret(finding extract.SecretFinding) {
	secretMu.Lock()
	if secretSeen == nil {
		secretSeen = make(map[string]bool)
	}
	key := finding.Rule + "|" + finding.MaskedValue + "|" + finding.URL
	if secretSeen[key] {
		secretMu.Unlock()
		return
	}
	secretSeen[key] = true
	SecretFindings = append(SecretFindings, finding)
	secretMu.Unlock()

	log.Logger.Warnf("[  secret  ] %s %s (%s)", finding.Rule, finding.MaskedValue, finding.URL)
	if conf.GlobalConfig.Quiet {
		if jsonData, err := json.Marshal(finding); err == nil {
			fmt.Println(string(jsonData))
		}
	}
}

// SnapshotSecrets 返回密钥命中列表的副本。
func SnapshotSecrets() []extract.SecretFinding {
	secretMu.Lock()
	defer secretMu.Unlock()
	out := make([]extract.SecretFinding, len(SecretFindings))
	copy(out, SecretFindings)
	return out
}

// ResetSecrets 每个目标开始前清空上一轮密钥记录。
func ResetSecrets() {
	secretMu.Lock()
	SecretFindings = nil
	secretSeen = make(map[string]bool)
	secretMu.Unlock()
}

func pushResult(pu *PendingUrl) {
	resultMu.Lock()
	q := ResultQueue
	resultMu.Unlock()
	if q == nil {
		return
	}
	// 先确认能入队再计数，否则 FlushResults 会等一个永远不会被消费的计数
	atomic.AddInt64(&resultPushCount, 1)
	q <- pu
}

// FlushResults 等待结果队列排空。
// fix: 浏览器可能被超时强制关闭，此时还有结果在队列里没被消费，
// 直接落盘就会丢尾巴；这里等「已入队」追平「已处理」。
func FlushResults(timeout time.Duration) {
	resultMu.Lock()
	q := ResultQueue
	resultMu.Unlock()
	if q == nil {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&resultHandledCount) >= atomic.LoadInt64(&resultPushCount) && len(q) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log.Logger.Warnf("flush results timeout, pending: %d",
		atomic.LoadInt64(&resultPushCount)-atomic.LoadInt64(&resultHandledCount))
}

// resultHandlerWork 处理结果队列。
// fix: 队列作为参数传入而不是读全局变量——每个目标都会重新初始化一次队列，
// 上一个目标的处理协程如果还在跑并读全局变量，就会和重新赋值产生数据竞争。
func resultHandlerWork(ctx context.Context, queue chan *PendingUrl) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-queue:
			if !ok {
				return
			}
			if conf.GlobalConfig.Quiet {
				var line interface{} = data
				if selected := filterResultFields(conf.GlobalConfig.ResultConf.Fields); selected != nil {
					line = filteredResultMap(data, selected)
				}
				jsonData, _ := json.Marshal(line)
				fmt.Println(string(jsonData))
			} else {
				resultMu.Lock()
				ResultList = append(ResultList, data)
				resultMu.Unlock()
				log.Logger.Infof("[%s] %s", data.Method, data.URL)
			}
			atomic.AddInt64(&resultHandledCount, 1)
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

// writeResultToJson 等写文件函数统一接收快照，避免遍历时结果列表被并发追加。
// 合法的 --fields 字段名（与 PendingUrl 字段一一对应）
var resultFieldNames = map[string]string{
	"url":           "URL",
	"method":        "Method",
	"host":          "Host",
	"data":          "Data",
	"status":        "Status",
	"request_str":   "RequestStr",
	"response_body": "ResponseBody",
}

// filterResultFields 按 --fields 过滤结果字段。
// fields 为空返回 nil（表示不过滤，序列化全字段）；非法字段名 Fatal 提示合法集合。
func filterResultFields(fields string) map[string]string {
	fields = strings.TrimSpace(fields)
	if fields == "" {
		return nil
	}
	selected := make(map[string]string)
	for _, name := range strings.Split(fields, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		field, ok := resultFieldNames[name]
		if !ok {
			valid := make([]string, 0, len(resultFieldNames))
			for k := range resultFieldNames {
				valid = append(valid, k)
			}
			sort.Strings(valid)
			log.Logger.Fatalf("--fields 非法字段 %q，合法字段: %s", name, strings.Join(valid, ","))
		}
		selected[name] = field
	}
	return selected
}

// filteredResultMap 把一条结果按选定字段构造成 map（json 输出用）。
// selected 为 nil 时返回 nil（调用方走全字段结构体序列化路径）。
func filteredResultMap(pu *PendingUrl, selected map[string]string) map[string]interface{} {
	if selected == nil {
		return nil
	}
	out := make(map[string]interface{}, len(selected))
	for name, field := range selected {
		v := reflect.ValueOf(pu).Elem().FieldByName(field)
		if !v.IsValid() {
			continue
		}
		out[name] = v.Interface()
	}
	return out
}

// marshalResult 序列化一条/一批结果：有字段过滤时构造 map 列表，否则原样。
func marshalResult(list []*PendingUrl, selected map[string]string) ([]byte, error) {
	if selected == nil {
		return json.MarshalIndent(list, "", "    ")
	}
	rows := make([]map[string]interface{}, 0, len(list))
	for _, pu := range list {
		rows = append(rows, filteredResultMap(pu, selected))
	}
	return json.MarshalIndent(rows, "", "    ")
}

// resultLine 渲染一条结果的 txt 行：--outputtemplate 模板优先，否则固定 [METHOD]URL。
// 模板执行失败降级为固定格式并记日志，不让单行错误中断整个落盘。
func resultLine(pu *PendingUrl) string {
	tplStr := strings.TrimSpace(conf.GlobalConfig.ResultConf.OutputTemplate)
	if tplStr == "" {
		return fmt.Sprintf("[%s]%s\n", pu.Method, pu.URL)
	}
	tpl, err := template.New("line").Parse(tplStr)
	if err != nil {
		log.Logger.Errorf("outputtemplate 解析错误，回退默认格式: %s", err)
		return fmt.Sprintf("[%s]%s\n", pu.Method, pu.URL)
	}
	var buf strings.Builder
	if err := tpl.Execute(&buf, pu); err != nil {
		log.Logger.Debugf("outputtemplate 渲染失败，回退默认格式: %s", err)
		return fmt.Sprintf("[%s]%s\n", pu.Method, pu.URL)
	}
	return buf.String() + "\n"
}

func writeResultToJson(name string, data *HtmlData) {
	jsonData, err := marshalResult(data.ResultList, filterResultFields(conf.GlobalConfig.ResultConf.Fields))
	if err != nil {
		log.Logger.Errorf("save result err: %s", err)
		return
	}
	writeResult(name, jsonData)
}

func writeResultToText(name string, data *HtmlData) {
	txtDate := ""
	for _, r := range data.ResultList {
		txtDate += resultLine(r)
	}
	writeResult(name, []byte(txtDate))
}

func writeResultToXlsx(name string, data *HtmlData) {
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
	for _, item := range data.ResultList {
		values := []string{
			item.Method,
			item.URL,
			item.Data,
			strconv.Itoa(item.Status),
		}

		row = sheet.AddRow()

		for _, value := range values {
			cell = row.AddCell()
			cell.Value = value
		}
	}
	err = xlsxFile.Save(name)

}

func writeResultToHtml(name string, data *HtmlData) {
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
	err = t.Execute(resultFile, data)
	if err != nil {
		log.Logger.Errorf(" %s file creation error: %s", name, err)
		return
	}
}

func (ei *EngineInfo) SaveResult() {
	log.Logger.Infof("[tab  count] %d", ei.GetTabCount())
	// 落盘前先排空结果队列，避免浏览器超时被强关时丢掉还在队列里的结果
	FlushResults(10 * time.Second)
	// 先拍快照，后面所有写文件都用这一份，不再直接遍历全局列表
	snapshot := ei.NewResultSnapshot()
	if snapshot.Count < 2 {
		log.Logger.Errorf("No content crawled, you can contact the developer to recar target: %s", ei.HostName)
		return
	}
	log.Logger.Infof("[  result  ] %d", snapshot.Count)

	// 如果指定了MergedOutput，优先使用它作为输出文件
	if conf.GlobalConfig.ResultConf.MergedOutput != "" {
		// 检查MergedOutput是否包含文件扩展名
		ext := path.Ext(conf.GlobalConfig.ResultConf.MergedOutput)
		if ext != "" {
			// 如果指定了扩展名，只保存对应格式
			format := strings.TrimPrefix(ext, ".")
			if _, ok := FormatMap[format]; ok {
				err := appendToFile(conf.GlobalConfig.ResultConf.MergedOutput, format, snapshot)
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
				if _, ok := FormatMap[format]; ok {
					fileName := baseName + "." + format
					filePath := path.Join(baseDir, fileName)
					err := appendToFile(filePath, format, snapshot)
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
		if _, ok := FormatMap[format]; ok {
			fileName := saveName + "." + format
			filePath := path.Join(ResultOutPutDir, fileName)
			FormatMap[format](filePath, snapshot)
			log.Logger.Infof("[   save   ] %s", filePath)
		} else {
			log.Logger.Errorf("format not found: %s", format)
		}
	}

	// 域外发现的 URL 单独输出（--outscope / scope.save_outscope 开启时）
	saveOutScope(conf.GlobalConfig.ScopeConf.SaveOutScope, ResultOutPutDir, saveName)
	// 密钥命中输出（--secrets 开启且有命中时）
	saveSecretResult(ResultOutPutDir, saveName)
}

// saveSecretResult 把密钥命中写到 <saveName>.secrets.txt / .secrets.json。
func saveSecretResult(outputDir string, saveName string) {
	findings := SnapshotSecrets()
	if len(findings) == 0 {
		return
	}
	txtLines := make([]string, 0, len(findings))
	for _, finding := range findings {
		txtLines = append(txtLines, fmt.Sprintf("[%s][%s] %s (%s)",
			finding.Severity, finding.Rule, finding.MaskedValue, finding.URL))
	}
	writeResult(path.Join(outputDir, saveName+".secrets.txt"), []byte(strings.Join(txtLines, "\n")+"\n"))

	jsonData, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		log.Logger.Errorf("marshal secrets err: %s", err)
		return
	}
	writeResult(path.Join(outputDir, saveName+".secrets.json"), jsonData)
	log.Logger.Infof("[   save   ] %s (%d secret findings)", path.Join(outputDir, saveName+".secrets.txt"), len(findings))
}

// saveOutScope 把爬取过程中记录的域外 URL 写到 <saveName>.outscope.txt。
// 这些是页面上出现但不属于爬取范围的外部资产，对攻击面梳理有价值。
func saveOutScope(enabled bool, outputDir string, saveName string) {
	if !enabled {
		return
	}
	outScopeUrls := scope.SnapshotOutScope()
	if len(outScopeUrls) == 0 {
		return
	}
	filePath := path.Join(outputDir, saveName+".outscope.txt")
	content := strings.Join(outScopeUrls, "\n") + "\n"
	writeResult(filePath, []byte(content))
	log.Logger.Infof("[   save   ] %s (%d out-of-scope urls)", filePath, len(outScopeUrls))
}

// appendToFile 根据不同格式追加内容到文件
func appendToFile(filePath string, format string, data *HtmlData) error {
	switch format {
	case "txt":
		return appendTxtResult(filePath, data.ResultList)
	case "json":
		return appendJsonResult(filePath, data.ResultList)
	case "xlsx":
		return appendXlsxResult(filePath, data.ResultList)
	case "html":
		return appendHtmlResult(filePath, data)
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
		_, err = fmt.Fprint(f, resultLine(r))
		if err != nil {
			return err
		}
	}
	return nil
}

// appendJsonResult 追加JSON格式结果。
// 追加路径统一用 map 结构（字段过滤与全字段共用一条逻辑，
// 旧文件里的结构体 JSON 反序列化成 map 同样成立，输出形状不变）。
func appendJsonResult(filePath string, results []*PendingUrl) error {
	var existingData []map[string]interface{}
	selected := filterResultFields(conf.GlobalConfig.ResultConf.Fields)

	if utils.IsExist(filePath) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &existingData); err != nil {
				return err
			}
		}
	}

	for _, r := range results {
		if selected == nil {
			// 全字段：map 里带上全部合法字段
			existingData = append(existingData, filteredResultMap(r, resultFieldNames))
		} else {
			existingData = append(existingData, filteredResultMap(r, selected))
		}
	}

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
	ResetResult()
	queue := make(chan *PendingUrl)
	resultMu.Lock()
	ResultQueue = queue
	resultMu.Unlock()
	FormatMap = make(map[string]FormatOutputFunc)
	go resultHandlerWork(ctx, queue)

	// 如果使用MergedOutput，使用追加模式的处理函数
	if conf.GlobalConfig.ResultConf.MergedOutput != "" {
		FormatMap["json"] = func(name string, data *HtmlData) {
			err := appendJsonResult(name, data.ResultList)
			if err != nil {
				log.Logger.Errorf("Failed to append json result: %v", err)
			}
		}
		FormatMap["txt"] = func(name string, data *HtmlData) {
			err := appendTxtResult(name, data.ResultList)
			if err != nil {
				log.Logger.Errorf("Failed to append txt result: %v", err)
			}
		}
		FormatMap["xlsx"] = func(name string, data *HtmlData) {
			err := appendXlsxResult(name, data.ResultList)
			if err != nil {
				log.Logger.Errorf("Failed to append xlsx result: %v", err)
			}
		}
		FormatMap["html"] = func(name string, data *HtmlData) {
			err := appendHtmlResult(name, data)
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
