package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	"argo/pkg/conf"

	"github.com/tealeg/xlsx"
)

type jsonSink struct{}

func (s *jsonSink) Name() string { return "json" }

func (s *jsonSink) Save(ei *EngineInfo, path string) error {
	data, err := json.MarshalIndent(ei.ResultList, "", "    ")
	if err != nil {
		return err
	}
	ei.writeResult(path, data)
	return nil
}

type textSink struct{}

func (s *textSink) Name() string { return "txt" }

func (s *textSink) Save(ei *EngineInfo, path string) error {
	txt := ""
	for _, r := range ei.ResultList {
		txt += fmt.Sprintf("[%s][%s]%s\t%s\n", r.Method, r.SourceType, r.URL, r.SourceUrl)
	}
	ei.writeResult(path, []byte(txt))
	return nil
}

type xlsxSink struct{}

func (s *xlsxSink) Name() string { return "xlsx" }

func (s *xlsxSink) Save(ei *EngineInfo, path string) error {
	xlsxFile := xlsx.NewFile()
	sheet, err := xlsxFile.AddSheet("Argo result")
	if err != nil {
		return err
	}
	titles := []string{"method", "url", "source_type", "source_url", "data", "status"}
	row := sheet.AddRow()
	for _, title := range titles {
		cell := row.AddCell()
		cell.Value = title
	}
	sheet.SetColWidth(0, 0, 8)  // method
	sheet.SetColWidth(1, 1, 80) // url
	sheet.SetColWidth(2, 2, 20) // source_type
	sheet.SetColWidth(3, 3, 80) // source_url
	sheet.SetColWidth(4, 4, 80) // data
	sheet.SetColWidth(5, 5, 8)  // status
	for _, data := range ei.ResultList {
		row = sheet.AddRow()
		values := []string{
			data.Method,
			data.URL,
			data.SourceType,
			data.SourceUrl,
			data.Data,
			strconv.Itoa(data.Status),
		}
		for _, value := range values {
			cell := row.AddCell()
			cell.Value = value
		}
	}
	return xlsxFile.Save(path)
}

type htmlSink struct{}

func (s *htmlSink) Name() string { return "html" }

func (s *htmlSink) Save(ei *EngineInfo, path string) error {
	t, err := template.New("result").Parse(ResultHtmlTemplate)
	if err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return t.Execute(file, ei.ResultHtmlData)
}

type jsonlSink struct{}

func (s *jsonlSink) Name() string { return "jsonl" }

func (s *jsonlSink) Save(ei *EngineInfo, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	for _, item := range ei.ResultList {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

func newMQSink(cfg conf.MQConf) ResultSink {
	return &mqSink{cfg: cfg, client: &http.Client{Timeout: 5 * time.Second}}
}

type mqSink struct {
	cfg    conf.MQConf
	client *http.Client
}

func (s *mqSink) Name() string { return "mq" }

func (s *mqSink) Save(ei *EngineInfo, _ string) error {
	if s.cfg.Type == "" {
		return nil
	}
	switch s.cfg.Type {
	case "http":
		return s.sendHTTP(ei.ResultList)
	default:
		return fmt.Errorf("unsupported mq type %s", s.cfg.Type)
	}
}

func (s *mqSink) sendHTTP(results []*PendingUrl) error {
	endpoint := s.cfg.Address
	if endpoint == "" {
		return fmt.Errorf("mq address empty")
	}
	if s.cfg.QueueName != "" {
		endpoint = fmt.Sprintf("%s/%s", strings.TrimRight(endpoint, "/"), strings.TrimLeft(s.cfg.QueueName, "/"))
	}
	for _, item := range results {
		body, err := json.Marshal(item)
		if err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("mq sink http status %d", resp.StatusCode)
		}
	}
	return nil
}
