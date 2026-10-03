package engine

import (
	"argo/pkg/conf"
	"argo/pkg/extract"
	"net/http"
)

// confExtractEnabled / confSecretsEnabled 小包装，供本文件内可读性。
func confExtractEnabled() bool { return conf.GlobalConfig.ExtractConf.Enable }
func confSecretsEnabled() bool { return conf.GlobalConfig.ExtractConf.Secrets }

// processExtractedResponse 对一个响应做二次提取（接口/密钥）。
//
// hijack 回调与 hybrid fetcher 共用同一段逻辑，保证两条路径的发现语义完全一致：
// 接口「解析即发现」直接记结果；文档类地址入爬取队列继续扩散。
func processExtractedResponse(responseURL string, contentType string, body []byte) {
	if !(confExtractEnabled() || confSecretsEnabled()) {
		return
	}
	input := &extract.ResponseInput{
		URL:         responseURL,
		ContentType: contentType,
		Body:        body,
	}
	if confExtractEnabled() {
		input.OnEndpoint = func(endpoint extract.Endpoint) {
			method := endpoint.Method
			if method == "" {
				method = http.MethodGet
			}
			// JS 里解析出的接口直接记为结果（与 katana 语义一致：
			// 解析即发现，不需要浏览器真的去请求）；方法取调用写法
			// （axios.post / fetch 的 method 选项），识别不出按 GET
			pushpendingNormalizeQueue(&PendingUrl{
				URL:     endpoint.URL,
				Method:  method,
				Headers: http.Header{},
			})
			// 只有文档类地址才值得开 tab 继续扩散链接，
			// 纯 API 接口开 tab 只会烧浏览器预算
			if extract.IsDocumentURL(endpoint.URL) {
				PushUrlQueue(&UrlInfo{Url: endpoint.URL, SourceType: "js extract", SourceUrl: responseURL, Depth: 0})
			}
		}
	}
	if confSecretsEnabled() {
		input.OnSecret = CollectSecret
	}
	input.Process()
}
