package engine

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"argo/pkg/conf"
)

func TestJSONSink(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "out.json")
	ei := &EngineInfo{ResultList: sampleResults()}
	sink := &jsonSink{}
	if err := sink.Save(ei, file); err != nil {
		t.Fatalf("json sink save err: %v", err)
	}
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), "https://target") {
		t.Fatalf("json sink missing url: %s", data)
	}
}

func TestTextSink(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "out.txt")
	ei := &EngineInfo{ResultList: sampleResults()}
	sink := &textSink{}
	if err := sink.Save(ei, file); err != nil {
		t.Fatalf("text sink save err: %v", err)
	}
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), "[GET]") {
		t.Fatalf("text sink missing method: %s", data)
	}
}

func TestJSONLSink(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "out.jsonl")
	ei := &EngineInfo{ResultList: sampleResults()}
	sink := &jsonlSink{}
	if err := sink.Save(ei, file); err != nil {
		t.Fatalf("jsonl sink save err: %v", err)
	}
	data, _ := os.ReadFile(file)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "https://target/") {
			t.Fatalf("jsonl sink line invalid: %s", line)
		}
		count++
	}
	if count != len(ei.ResultList) {
		t.Fatalf("jsonl sink expected %d lines got %d", len(ei.ResultList), count)
	}
}

func TestMQSinkHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg := conf.MQConf{Type: "http", Address: server.URL, QueueName: "results"}
	ei := &EngineInfo{ResultList: sampleResults()}
	sink := newMQSink(cfg)
	if err := sink.Save(ei, ""); err != nil {
		t.Fatalf("mq sink save err: %v", err)
	}
}

func sampleResults() []*PendingUrl {
	return []*PendingUrl{
		{Method: "GET", URL: "https://target/1", Data: "", Status: 200},
		{Method: "POST", URL: "https://target/2", Data: "a=1", Status: 201},
	}
}
