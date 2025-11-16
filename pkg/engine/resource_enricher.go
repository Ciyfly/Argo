package engine

import (
	"bufio"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"argo/pkg/log"
	"argo/pkg/utils"
)

const maxResourceSize = 512 * 1024

var resourceExtParsers = map[string]func(base string, data []byte) []string{
	".js":   parseJSResource,
	".mjs":  parseJSResource,
	".json": parseGenericResource,
	".xml":  parseGenericResource,
	".csv":  parseGenericResource,
}

func (ei *EngineInfo) EnrichStaticResource(source string, depth int) {
	if source == "" || ei == nil {
		return
	}
	if !ei.reserveResourceTask(source) {
		return
	}
	go ei.fetchAndParseResource(source, depth)
}

func (ei *EngineInfo) reserveResourceTask(source string) bool {
	ei.resourceMu.Lock()
	defer ei.resourceMu.Unlock()
	if ei.resourceVisited == nil {
		ei.resourceVisited = make(map[string]struct{})
	}
	if _, ok := ei.resourceVisited[source]; ok {
		return false
	}
	ei.resourceVisited[source] = struct{}{}
	return true
}

func (ei *EngineInfo) fetchAndParseResource(source string, depth int) {
	parser := selectResourceParser(source)
	if parser == nil {
		return
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return
	}
	if parsed.Hostname() != ei.HostName {
		return
	}
	client := ei.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodGet, source, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Logger.Debugf("[resource] fetch err %s %v", source, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return
	}
	reader := bufio.NewReader(io.LimitReader(resp.Body, maxResourceSize))
	data, err := io.ReadAll(reader)
	if err != nil {
		return
	}
	urls := parser(source, data)
	if len(urls) == 0 {
		return
	}
	for _, target := range urls {
		if target == "" {
			continue
		}
		ei.PushStaticUrl(&UrlInfo{Url: target, SourceType: "resource", SourceUrl: source, Depth: depth})
	}
}

func selectResourceParser(source string) func(base string, data []byte) []string {
	raw := source
	if idx := strings.Index(raw, "?"); idx != -1 {
		raw = raw[:idx]
	}
	ext := strings.ToLower(path.Ext(raw))
	if ext == "" {
		return nil
	}
	return resourceExtParsers[ext]
}

var absoluteURLPattern = utils.MustCompileURLRegex()
var jsKeywordPattern = utils.MustCompileJSKeywordRegex()

func parseJSResource(base string, data []byte) []string {
	results := make([]string, 0)
	text := string(data)
	for _, match := range absoluteURLPattern.FindAllString(text, -1) {
		if url, err := utils.CanonicalizeURL(match, base); err == nil {
			results = append(results, url)
		}
	}
	matches := jsKeywordPattern.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		candidate := m[2]
		if url, err := utils.CanonicalizeURL(candidate, base); err == nil {
			results = append(results, url)
		}
	}
	return utils.UniqueStrings(results)
}

func parseGenericResource(base string, data []byte) []string {
	results := make([]string, 0)
	text := string(data)
	for _, match := range absoluteURLPattern.FindAllString(text, -1) {
		if url, err := utils.CanonicalizeURL(match, base); err == nil {
			results = append(results, url)
		}
	}
	return utils.UniqueStrings(results)
}
