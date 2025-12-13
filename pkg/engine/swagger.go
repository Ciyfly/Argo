// Package engine provides Swagger/OpenAPI endpoint discovery and parsing
// P3优化: Swagger/OpenAPI 支持 - 自动发现端点、解析规范、提取 API 接口
package engine

import (
	"argo/pkg/log"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// SwaggerConfig Swagger 发现配置
type SwaggerConfig struct {
	Enabled           bool          `yaml:"enabled" json:"enabled"`
	AutoDiscover      bool          `yaml:"auto_discover" json:"auto_discover"`             // 自动发现端点
	ParseSpec         bool          `yaml:"parse_spec" json:"parse_spec"`                   // 解析规范文件
	GenerateRequests  bool          `yaml:"generate_requests" json:"generate_requests"`    // 生成请求示例
	Timeout           time.Duration `yaml:"timeout" json:"timeout"`                         // 请求超时
	FollowBasePath    bool          `yaml:"follow_base_path" json:"follow_base_path"`       // 使用 basePath
	ExtractFromHTML   bool          `yaml:"extract_from_html" json:"extract_from_html"`     // 从HTML提取
}

// DefaultSwaggerConfig 返回默认配置
func DefaultSwaggerConfig() *SwaggerConfig {
	return &SwaggerConfig{
		Enabled:          true,
		AutoDiscover:     true,
		ParseSpec:        true,
		GenerateRequests: true,
		Timeout:          15 * time.Second,
		FollowBasePath:   true,
		ExtractFromHTML:  true,
	}
}

// SwaggerDiscoverer Swagger 发现器
type SwaggerDiscoverer struct {
	config    *SwaggerConfig
	client    *http.Client
	mu        sync.RWMutex
	specs     map[string]*SwaggerSpec // key: URL
	endpoints []*SwaggerEndpoint
	engine    *EngineInfo
}

// SwaggerSpec Swagger/OpenAPI 规范
type SwaggerSpec struct {
	URL            string                 `json:"url"`
	Version        string                 `json:"version"`         // "2.0" 或 "3.x.x"
	Title          string                 `json:"title"`
	Description    string                 `json:"description,omitempty"`
	BasePath       string                 `json:"base_path,omitempty"`
	Host           string                 `json:"host,omitempty"`
	Schemes        []string               `json:"schemes,omitempty"`
	Paths          map[string]*PathItem   `json:"paths"`
	Tags           []string               `json:"tags,omitempty"`
	SecurityDefs   map[string]interface{} `json:"security_definitions,omitempty"`
	DiscoveredAt   time.Time              `json:"discovered_at"`
	EndpointCount  int                    `json:"endpoint_count"`
	Raw            map[string]interface{} `json:"raw,omitempty"`
}

// PathItem 路径项
type PathItem struct {
	Get     *Operation `json:"get,omitempty"`
	Post    *Operation `json:"post,omitempty"`
	Put     *Operation `json:"put,omitempty"`
	Delete  *Operation `json:"delete,omitempty"`
	Patch   *Operation `json:"patch,omitempty"`
	Options *Operation `json:"options,omitempty"`
	Head    *Operation `json:"head,omitempty"`
}

// Operation API 操作
type Operation struct {
	OperationID string       `json:"operation_id,omitempty"`
	Summary     string       `json:"summary,omitempty"`
	Description string       `json:"description,omitempty"`
	Tags        []string     `json:"tags,omitempty"`
	Parameters  []*Parameter `json:"parameters,omitempty"`
	RequestBody *RequestBody `json:"request_body,omitempty"`
	Responses   map[string]*Response `json:"responses,omitempty"`
	Security    []map[string][]string `json:"security,omitempty"`
	Deprecated  bool         `json:"deprecated,omitempty"`
	Consumes    []string     `json:"consumes,omitempty"`
	Produces    []string     `json:"produces,omitempty"`
}

// Parameter API 参数
type Parameter struct {
	Name        string      `json:"name"`
	In          string      `json:"in"` // query, path, header, body, formData
	Description string      `json:"description,omitempty"`
	Required    bool        `json:"required"`
	Type        string      `json:"type,omitempty"`
	Format      string      `json:"format,omitempty"`
	Schema      *SchemaRef  `json:"schema,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Enum        []interface{} `json:"enum,omitempty"`
}

// RequestBody 请求体 (OpenAPI 3.x)
type RequestBody struct {
	Description string                 `json:"description,omitempty"`
	Required    bool                   `json:"required"`
	Content     map[string]*MediaType  `json:"content,omitempty"`
}

// MediaType 媒体类型
type MediaType struct {
	Schema   *SchemaRef             `json:"schema,omitempty"`
	Example  interface{}            `json:"example,omitempty"`
	Examples map[string]interface{} `json:"examples,omitempty"`
}

// Response API 响应
type Response struct {
	Description string                `json:"description"`
	Schema      *SchemaRef            `json:"schema,omitempty"`
	Content     map[string]*MediaType `json:"content,omitempty"`
	Headers     map[string]*Parameter `json:"headers,omitempty"`
}

// SchemaRef Schema 引用
type SchemaRef struct {
	Ref        string                 `json:"$ref,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Format     string                 `json:"format,omitempty"`
	Properties map[string]*SchemaRef  `json:"properties,omitempty"`
	Items      *SchemaRef             `json:"items,omitempty"`
	Required   []string               `json:"required,omitempty"`
	Enum       []interface{}          `json:"enum,omitempty"`
	Example    interface{}            `json:"example,omitempty"`
}

// APIEndpoint 发现的 API 端点
type SwaggerEndpoint struct {
	URL         string            `json:"url"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Summary     string            `json:"summary,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Parameters  []*Parameter      `json:"parameters,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Security    []string          `json:"security,omitempty"`
	Deprecated  bool              `json:"deprecated"`
	Source      string            `json:"source"` // swagger_2.0, openapi_3.x, html_link
	SpecURL     string            `json:"spec_url,omitempty"`
	SampleRequest *SampleRequest  `json:"sample_request,omitempty"`
}

// SampleRequest 示例请求
type SampleRequest struct {
	URL         string            `json:"url"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers,omitempty"`
	QueryParams map[string]string `json:"query_params,omitempty"`
	Body        string            `json:"body,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
}

// SwaggerStats Swagger 统计
type SwaggerStats struct {
	SpecsDiscovered   int `json:"specs_discovered"`
	SpecsParsed       int `json:"specs_parsed"`
	EndpointsFound    int `json:"endpoints_found"`
	Swagger2Count     int `json:"swagger_2_count"`
	OpenAPI3Count     int `json:"openapi_3_count"`
	PathsTotal        int `json:"paths_total"`
	OperationsTotal   int `json:"operations_total"`
}

// 常见 Swagger/OpenAPI 端点路径
var commonSwaggerPaths = []string{
	// Swagger 2.0
	"/swagger.json",
	"/swagger.yaml",
	"/swagger/swagger.json",
	"/swagger/swagger.yaml",
	"/api/swagger.json",
	"/api/swagger.yaml",
	"/v1/swagger.json",
	"/v2/swagger.json",
	"/v3/swagger.json",
	"/api-docs",
	"/api-docs.json",
	"/api-docs.yaml",
	"/swagger/v1/swagger.json",
	"/swagger/v2/swagger.json",

	// OpenAPI 3.x
	"/openapi.json",
	"/openapi.yaml",
	"/api/openapi.json",
	"/api/openapi.yaml",
	"/v3/api-docs",
	"/v3/api-docs.yaml",
	"/openapi/v3/api-docs",

	// Spring Boot / Java
	"/v2/api-docs",
	"/v3/api-docs",
	"/swagger-resources",
	"/swagger-resources/configuration/ui",
	"/swagger-resources/configuration/security",

	// .NET / ASP.NET
	"/swagger/v1/swagger.json",
	"/swagger/v2/swagger.json",

	// 其他常见路径
	"/api/spec",
	"/api/spec.json",
	"/api/spec.yaml",
	"/docs/api.json",
	"/docs/api.yaml",
	"/documentation/json",
	"/documentation/yaml",
	"/_swagger",
	"/_swagger.json",
	"/rest/api-docs",
	"/api.json",
	"/api.yaml",
	"/spec.json",
	"/spec.yaml",
}

// Swagger UI 路径
var swaggerUIPaths = []string{
	"/swagger-ui.html",
	"/swagger-ui/",
	"/swagger-ui/index.html",
	"/swagger/",
	"/swagger/index.html",
	"/api/swagger-ui.html",
	"/api/swagger-ui/",
	"/docs",
	"/docs/",
	"/api-docs/",
	"/documentation",
	"/documentation/",
	"/redoc",
	"/redoc/",
	"/rapidoc",
	"/rapidoc/",
}

// HTML 中 Swagger 规范链接的正则
var swaggerURLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)["'](https?://[^"']*swagger[^"']*\.(?:json|yaml))["']`),
	regexp.MustCompile(`(?i)["'](https?://[^"']*openapi[^"']*\.(?:json|yaml))["']`),
	regexp.MustCompile(`(?i)["'](https?://[^"']*api-docs[^"']*)["']`),
	regexp.MustCompile(`(?i)["']([^"']*swagger[^"']*\.(?:json|yaml))["']`),
	regexp.MustCompile(`(?i)["']([^"']*openapi[^"']*\.(?:json|yaml))["']`),
	regexp.MustCompile(`(?i)url\s*:\s*["']([^"']+)["']`), // Swagger UI config
	regexp.MustCompile(`(?i)spec-url\s*=\s*["']([^"']+)["']`), // Redoc
}

// NewSwaggerDiscoverer 创建 Swagger 发现器
func NewSwaggerDiscoverer(engine *EngineInfo, config *SwaggerConfig) *SwaggerDiscoverer {
	if config == nil {
		config = DefaultSwaggerConfig()
	}

	return &SwaggerDiscoverer{
		config:    config,
		client:    &http.Client{Timeout: config.Timeout},
		specs:     make(map[string]*SwaggerSpec),
		endpoints: make([]*SwaggerEndpoint, 0),
		engine:    engine,
	}
}

// DiscoverFromBaseURL 从基础 URL 发现 Swagger 规范
func (sd *SwaggerDiscoverer) DiscoverFromBaseURL(baseURL string) []*SwaggerSpec {
	if !sd.config.Enabled || !sd.config.AutoDiscover {
		return nil
	}

	discovered := make([]*SwaggerSpec, 0)

	u, err := url.Parse(baseURL)
	if err != nil {
		log.Logger.Debugf("swagger: invalid base URL: %s", baseURL)
		return nil
	}

	base := fmt.Sprintf("%s://%s", u.Scheme, u.Host)

	// 1. 尝试常见的 Swagger 规范路径
	for _, path := range commonSwaggerPaths {
		specURL := base + path
		if spec := sd.fetchAndParseSpec(specURL); spec != nil {
			discovered = append(discovered, spec)
			log.Logger.Infof("swagger: discovered spec at %s (version: %s, endpoints: %d)",
				specURL, spec.Version, spec.EndpointCount)
		}
	}

	// 2. 检查 Swagger UI 页面并提取规范 URL
	if sd.config.ExtractFromHTML {
		for _, uiPath := range swaggerUIPaths {
			uiURL := base + uiPath
			specURLs := sd.extractSpecURLsFromUI(uiURL)
			for _, specURL := range specURLs {
				// 处理相对路径
				if !strings.HasPrefix(specURL, "http") {
					specURL = base + specURL
				}
				if spec := sd.fetchAndParseSpec(specURL); spec != nil {
					discovered = append(discovered, spec)
					log.Logger.Infof("swagger: discovered spec from UI at %s", specURL)
				}
			}
		}
	}

	return discovered
}

// extractSpecURLsFromUI 从 Swagger UI 页面提取规范 URL
func (sd *SwaggerDiscoverer) extractSpecURLsFromUI(uiURL string) []string {
	req, err := http.NewRequest("GET", uiURL, nil)
	if err != nil {
		return nil
	}

	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ArgoBot/1.0)")

	resp, err := sd.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	content := string(body)
	urls := make([]string, 0)
	seen := make(map[string]bool)

	for _, pattern := range swaggerURLPatterns {
		matches := pattern.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 {
				specURL := match[1]
				if !seen[specURL] {
					seen[specURL] = true
					urls = append(urls, specURL)
				}
			}
		}
	}

	return urls
}

// fetchAndParseSpec 获取并解析规范
func (sd *SwaggerDiscoverer) fetchAndParseSpec(specURL string) *SwaggerSpec {
	sd.mu.RLock()
	if _, exists := sd.specs[specURL]; exists {
		sd.mu.RUnlock()
		return nil // 已经解析过
	}
	sd.mu.RUnlock()

	req, err := http.NewRequest("GET", specURL, nil)
	if err != nil {
		return nil
	}

	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ArgoBot/1.0)")

	resp, err := sd.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	// 尝试解析为 JSON 或 YAML
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		// 尝试 YAML
		if err := yaml.Unmarshal(body, &raw); err != nil {
			return nil
		}
	}

	// 验证是否为 Swagger/OpenAPI 规范
	spec := sd.parseSpec(raw, specURL)
	if spec == nil {
		return nil
	}

	sd.mu.Lock()
	sd.specs[specURL] = spec
	sd.mu.Unlock()

	// 提取端点
	if sd.config.ParseSpec {
		sd.extractEndpoints(spec, specURL)
	}

	return spec
}

// parseSpec 解析规范
func (sd *SwaggerDiscoverer) parseSpec(raw map[string]interface{}, specURL string) *SwaggerSpec {
	spec := &SwaggerSpec{
		URL:          specURL,
		DiscoveredAt: time.Now(),
		Paths:        make(map[string]*PathItem),
		Raw:          raw,
	}

	// 检测版本
	if swagger, ok := raw["swagger"].(string); ok {
		spec.Version = swagger // Swagger 2.0
	} else if openapi, ok := raw["openapi"].(string); ok {
		spec.Version = openapi // OpenAPI 3.x
	} else {
		return nil // 不是有效的规范
	}

	// 解析 info
	if info, ok := raw["info"].(map[string]interface{}); ok {
		if title, ok := info["title"].(string); ok {
			spec.Title = title
		}
		if desc, ok := info["description"].(string); ok {
			spec.Description = desc
		}
	}

	// Swagger 2.0 特有字段
	if strings.HasPrefix(spec.Version, "2") {
		if host, ok := raw["host"].(string); ok {
			spec.Host = host
		}
		if basePath, ok := raw["basePath"].(string); ok {
			spec.BasePath = basePath
		}
		if schemes, ok := raw["schemes"].([]interface{}); ok {
			for _, s := range schemes {
				if str, ok := s.(string); ok {
					spec.Schemes = append(spec.Schemes, str)
				}
			}
		}
	}

	// OpenAPI 3.x servers
	if servers, ok := raw["servers"].([]interface{}); ok {
		for _, s := range servers {
			if server, ok := s.(map[string]interface{}); ok {
				if serverURL, ok := server["url"].(string); ok {
					// 提取 host 和 basePath
					if u, err := url.Parse(serverURL); err == nil {
						spec.Host = u.Host
						spec.BasePath = u.Path
					}
					break
				}
			}
		}
	}

	// 解析 paths
	if paths, ok := raw["paths"].(map[string]interface{}); ok {
		for path, pathData := range paths {
			if pathObj, ok := pathData.(map[string]interface{}); ok {
				pathItem := sd.parsePathItem(pathObj)
				spec.Paths[path] = pathItem
				spec.EndpointCount += countOperations(pathItem)
			}
		}
	}

	// 解析 tags
	if tags, ok := raw["tags"].([]interface{}); ok {
		for _, t := range tags {
			if tag, ok := t.(map[string]interface{}); ok {
				if name, ok := tag["name"].(string); ok {
					spec.Tags = append(spec.Tags, name)
				}
			}
		}
	}

	// 解析 securityDefinitions (Swagger 2.0) 或 components.securitySchemes (OpenAPI 3.x)
	if secDefs, ok := raw["securityDefinitions"].(map[string]interface{}); ok {
		spec.SecurityDefs = secDefs
	} else if components, ok := raw["components"].(map[string]interface{}); ok {
		if secSchemes, ok := components["securitySchemes"].(map[string]interface{}); ok {
			spec.SecurityDefs = secSchemes
		}
	}

	return spec
}

// parsePathItem 解析路径项
func (sd *SwaggerDiscoverer) parsePathItem(pathObj map[string]interface{}) *PathItem {
	item := &PathItem{}

	methods := map[string]**Operation{
		"get":     &item.Get,
		"post":    &item.Post,
		"put":     &item.Put,
		"delete":  &item.Delete,
		"patch":   &item.Patch,
		"options": &item.Options,
		"head":    &item.Head,
	}

	for method, opPtr := range methods {
		if opData, ok := pathObj[method].(map[string]interface{}); ok {
			*opPtr = sd.parseOperation(opData)
		}
	}

	return item
}

// parseOperation 解析操作
func (sd *SwaggerDiscoverer) parseOperation(opData map[string]interface{}) *Operation {
	op := &Operation{
		Parameters: make([]*Parameter, 0),
		Responses:  make(map[string]*Response),
	}

	if id, ok := opData["operationId"].(string); ok {
		op.OperationID = id
	}
	if summary, ok := opData["summary"].(string); ok {
		op.Summary = summary
	}
	if desc, ok := opData["description"].(string); ok {
		op.Description = desc
	}
	if deprecated, ok := opData["deprecated"].(bool); ok {
		op.Deprecated = deprecated
	}

	// Tags
	if tags, ok := opData["tags"].([]interface{}); ok {
		for _, t := range tags {
			if tag, ok := t.(string); ok {
				op.Tags = append(op.Tags, tag)
			}
		}
	}

	// Consumes/Produces (Swagger 2.0)
	if consumes, ok := opData["consumes"].([]interface{}); ok {
		for _, c := range consumes {
			if ct, ok := c.(string); ok {
				op.Consumes = append(op.Consumes, ct)
			}
		}
	}
	if produces, ok := opData["produces"].([]interface{}); ok {
		for _, p := range produces {
			if pt, ok := p.(string); ok {
				op.Produces = append(op.Produces, pt)
			}
		}
	}

	// Parameters
	if params, ok := opData["parameters"].([]interface{}); ok {
		for _, p := range params {
			if paramData, ok := p.(map[string]interface{}); ok {
				param := sd.parseParameter(paramData)
				if param != nil {
					op.Parameters = append(op.Parameters, param)
				}
			}
		}
	}

	// RequestBody (OpenAPI 3.x)
	if reqBody, ok := opData["requestBody"].(map[string]interface{}); ok {
		op.RequestBody = sd.parseRequestBody(reqBody)
	}

	// Security
	if security, ok := opData["security"].([]interface{}); ok {
		for _, s := range security {
			if secMap, ok := s.(map[string]interface{}); ok {
				secItem := make(map[string][]string)
				for name, scopes := range secMap {
					scopeList := []string{}
					if scopeArr, ok := scopes.([]interface{}); ok {
						for _, scope := range scopeArr {
							if scopeStr, ok := scope.(string); ok {
								scopeList = append(scopeList, scopeStr)
							}
						}
					}
					secItem[name] = scopeList
				}
				op.Security = append(op.Security, secItem)
			}
		}
	}

	return op
}

// parseParameter 解析参数
func (sd *SwaggerDiscoverer) parseParameter(paramData map[string]interface{}) *Parameter {
	param := &Parameter{}

	if name, ok := paramData["name"].(string); ok {
		param.Name = name
	} else {
		return nil
	}

	if in, ok := paramData["in"].(string); ok {
		param.In = in
	}
	if desc, ok := paramData["description"].(string); ok {
		param.Description = desc
	}
	if required, ok := paramData["required"].(bool); ok {
		param.Required = required
	}
	if typ, ok := paramData["type"].(string); ok {
		param.Type = typ
	}
	if format, ok := paramData["format"].(string); ok {
		param.Format = format
	}
	if def, ok := paramData["default"]; ok {
		param.Default = def
	}

	// Enum
	if enum, ok := paramData["enum"].([]interface{}); ok {
		param.Enum = enum
	}

	// Schema
	if schema, ok := paramData["schema"].(map[string]interface{}); ok {
		param.Schema = sd.parseSchemaRef(schema)
	}

	return param
}

// parseRequestBody 解析请求体
func (sd *SwaggerDiscoverer) parseRequestBody(reqBody map[string]interface{}) *RequestBody {
	rb := &RequestBody{
		Content: make(map[string]*MediaType),
	}

	if desc, ok := reqBody["description"].(string); ok {
		rb.Description = desc
	}
	if required, ok := reqBody["required"].(bool); ok {
		rb.Required = required
	}

	if content, ok := reqBody["content"].(map[string]interface{}); ok {
		for mediaType, mediaData := range content {
			if mediaObj, ok := mediaData.(map[string]interface{}); ok {
				mt := &MediaType{}
				if schema, ok := mediaObj["schema"].(map[string]interface{}); ok {
					mt.Schema = sd.parseSchemaRef(schema)
				}
				if example, ok := mediaObj["example"]; ok {
					mt.Example = example
				}
				rb.Content[mediaType] = mt
			}
		}
	}

	return rb
}

// parseSchemaRef 解析 Schema 引用
func (sd *SwaggerDiscoverer) parseSchemaRef(schemaData map[string]interface{}) *SchemaRef {
	schema := &SchemaRef{}

	if ref, ok := schemaData["$ref"].(string); ok {
		schema.Ref = ref
	}
	if typ, ok := schemaData["type"].(string); ok {
		schema.Type = typ
	}
	if format, ok := schemaData["format"].(string); ok {
		schema.Format = format
	}
	if example, ok := schemaData["example"]; ok {
		schema.Example = example
	}

	// Properties
	if props, ok := schemaData["properties"].(map[string]interface{}); ok {
		schema.Properties = make(map[string]*SchemaRef)
		for name, propData := range props {
			if propObj, ok := propData.(map[string]interface{}); ok {
				schema.Properties[name] = sd.parseSchemaRef(propObj)
			}
		}
	}

	// Items (for arrays)
	if items, ok := schemaData["items"].(map[string]interface{}); ok {
		schema.Items = sd.parseSchemaRef(items)
	}

	// Required
	if required, ok := schemaData["required"].([]interface{}); ok {
		for _, r := range required {
			if reqStr, ok := r.(string); ok {
				schema.Required = append(schema.Required, reqStr)
			}
		}
	}

	// Enum
	if enum, ok := schemaData["enum"].([]interface{}); ok {
		schema.Enum = enum
	}

	return schema
}

// extractEndpoints 从规范中提取端点
func (sd *SwaggerDiscoverer) extractEndpoints(spec *SwaggerSpec, specURL string) {
	baseURL := sd.buildBaseURL(spec, specURL)
	source := "swagger_2.0"
	if strings.HasPrefix(spec.Version, "3") {
		source = "openapi_3.x"
	}

	for path, pathItem := range spec.Paths {
		methods := map[string]*Operation{
			"GET":     pathItem.Get,
			"POST":    pathItem.Post,
			"PUT":     pathItem.Put,
			"DELETE":  pathItem.Delete,
			"PATCH":   pathItem.Patch,
			"OPTIONS": pathItem.Options,
			"HEAD":    pathItem.Head,
		}

		for method, op := range methods {
			if op == nil {
				continue
			}

			endpoint := &SwaggerEndpoint{
				URL:         baseURL + path,
				Method:      method,
				Path:        path,
				Summary:     op.Summary,
				Description: op.Description,
				Tags:        op.Tags,
				Parameters:  op.Parameters,
				Deprecated:  op.Deprecated,
				Source:      source,
				SpecURL:     specURL,
			}

			// 确定 Content-Type
			if len(op.Consumes) > 0 {
				endpoint.ContentType = op.Consumes[0]
			} else if op.RequestBody != nil {
				for ct := range op.RequestBody.Content {
					endpoint.ContentType = ct
					break
				}
			}

			// 提取安全要求
			for _, secReq := range op.Security {
				for secName := range secReq {
					endpoint.Security = append(endpoint.Security, secName)
				}
			}

			// 生成示例请求
			if sd.config.GenerateRequests {
				endpoint.SampleRequest = sd.generateSampleRequest(endpoint, op)
			}

			sd.addEndpoint(endpoint)
		}
	}
}

// buildBaseURL 构建基础 URL
func (sd *SwaggerDiscoverer) buildBaseURL(spec *SwaggerSpec, specURL string) string {
	if !sd.config.FollowBasePath {
		// 使用规范文件所在的 host
		u, _ := url.Parse(specURL)
		return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	}

	// 使用规范中定义的 host 和 basePath
	u, _ := url.Parse(specURL)
	scheme := u.Scheme
	host := u.Host

	if spec.Host != "" {
		host = spec.Host
	}

	if len(spec.Schemes) > 0 {
		scheme = spec.Schemes[0]
	}

	basePath := spec.BasePath
	if basePath == "/" {
		basePath = ""
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, basePath)
}

// generateSampleRequest 生成示例请求
func (sd *SwaggerDiscoverer) generateSampleRequest(endpoint *SwaggerEndpoint, op *Operation) *SampleRequest {
	sample := &SampleRequest{
		URL:         endpoint.URL,
		Method:      endpoint.Method,
		Headers:     make(map[string]string),
		QueryParams: make(map[string]string),
	}

	// 设置默认 Content-Type
	if endpoint.ContentType != "" {
		sample.ContentType = endpoint.ContentType
		sample.Headers["Content-Type"] = endpoint.ContentType
	}

	// 处理参数
	pathURL := endpoint.URL
	bodyParams := make(map[string]interface{})

	for _, param := range endpoint.Parameters {
		sampleValue := sd.getSampleValue(param)

		switch param.In {
		case "path":
			pathURL = strings.ReplaceAll(pathURL, "{"+param.Name+"}", fmt.Sprintf("%v", sampleValue))
		case "query":
			sample.QueryParams[param.Name] = fmt.Sprintf("%v", sampleValue)
		case "header":
			sample.Headers[param.Name] = fmt.Sprintf("%v", sampleValue)
		case "body":
			if param.Schema != nil {
				bodyParams = sd.generateBodyFromSchema(param.Schema)
			}
		case "formData":
			bodyParams[param.Name] = sampleValue
		}
	}

	// OpenAPI 3.x requestBody
	if op.RequestBody != nil {
		for _, mediaType := range op.RequestBody.Content {
			if mediaType.Schema != nil {
				bodyParams = sd.generateBodyFromSchema(mediaType.Schema)
			}
			if mediaType.Example != nil {
				bodyParams = mediaType.Example.(map[string]interface{})
			}
			break
		}
	}

	sample.URL = pathURL

	// 构建查询字符串
	if len(sample.QueryParams) > 0 {
		queryParts := make([]string, 0)
		for k, v := range sample.QueryParams {
			queryParts = append(queryParts, fmt.Sprintf("%s=%s", url.QueryEscape(k), url.QueryEscape(v)))
		}
		sample.URL = pathURL + "?" + strings.Join(queryParts, "&")
	}

	// 生成请求体
	if len(bodyParams) > 0 {
		if bodyJSON, err := json.MarshalIndent(bodyParams, "", "  "); err == nil {
			sample.Body = string(bodyJSON)
		}
	}

	return sample
}

// getSampleValue 获取参数示例值
func (sd *SwaggerDiscoverer) getSampleValue(param *Parameter) interface{} {
	// 优先使用 default 或 enum
	if param.Default != nil {
		return param.Default
	}
	if len(param.Enum) > 0 {
		return param.Enum[0]
	}

	// 根据类型生成示例值
	switch param.Type {
	case "integer", "int", "int32", "int64":
		return 1
	case "number", "float", "double":
		return 1.0
	case "boolean", "bool":
		return true
	case "array":
		return []interface{}{}
	case "object":
		return map[string]interface{}{}
	default:
		// 根据参数名生成有意义的值
		name := strings.ToLower(param.Name)
		switch {
		case strings.Contains(name, "id"):
			return "1"
		case strings.Contains(name, "name"):
			return "test"
		case strings.Contains(name, "email"):
			return "test@example.com"
		case strings.Contains(name, "phone"):
			return "13800138000"
		case strings.Contains(name, "page"):
			return "1"
		case strings.Contains(name, "limit") || strings.Contains(name, "size"):
			return "10"
		case strings.Contains(name, "token"):
			return "test_token"
		case strings.Contains(name, "key"):
			return "test_key"
		case strings.Contains(name, "date"):
			return time.Now().Format("2006-01-02")
		case strings.Contains(name, "time"):
			return time.Now().Format(time.RFC3339)
		default:
			return "test"
		}
	}
}

// generateBodyFromSchema 从 Schema 生成请求体
func (sd *SwaggerDiscoverer) generateBodyFromSchema(schema *SchemaRef) map[string]interface{} {
	body := make(map[string]interface{})

	if schema == nil {
		return body
	}

	// 处理 $ref (简化处理，只返回空对象)
	if schema.Ref != "" {
		return body
	}

	// 使用 example
	if schema.Example != nil {
		if m, ok := schema.Example.(map[string]interface{}); ok {
			return m
		}
	}

	// 根据 properties 生成
	for name, prop := range schema.Properties {
		body[name] = sd.getSchemaValue(prop, name)
	}

	return body
}

// getSchemaValue 获取 Schema 示例值
func (sd *SwaggerDiscoverer) getSchemaValue(schema *SchemaRef, name string) interface{} {
	if schema == nil {
		return nil
	}

	if schema.Example != nil {
		return schema.Example
	}

	if len(schema.Enum) > 0 {
		return schema.Enum[0]
	}

	switch schema.Type {
	case "integer", "int", "int32", "int64":
		return 1
	case "number", "float", "double":
		return 1.0
	case "boolean", "bool":
		return true
	case "array":
		if schema.Items != nil {
			return []interface{}{sd.getSchemaValue(schema.Items, "")}
		}
		return []interface{}{}
	case "object":
		obj := make(map[string]interface{})
		for propName, propSchema := range schema.Properties {
			obj[propName] = sd.getSchemaValue(propSchema, propName)
		}
		return obj
	default:
		// 根据字段名生成
		nameLower := strings.ToLower(name)
		switch {
		case strings.Contains(nameLower, "email"):
			return "test@example.com"
		case strings.Contains(nameLower, "phone"):
			return "13800138000"
		case strings.Contains(nameLower, "name"):
			return "test"
		case strings.Contains(nameLower, "id"):
			return "1"
		default:
			return "string"
		}
	}
}

// addEndpoint 添加端点
func (sd *SwaggerDiscoverer) addEndpoint(endpoint *SwaggerEndpoint) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.endpoints = append(sd.endpoints, endpoint)
}

// AnalyzeContent 分析内容中的 Swagger 信息
func (sd *SwaggerDiscoverer) AnalyzeContent(content string, sourceURL string) {
	if !sd.config.Enabled {
		return
	}

	// 检测内容中的 Swagger 规范 URL
	for _, pattern := range swaggerURLPatterns {
		matches := pattern.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 {
				specURL := match[1]
				// 处理相对路径
				if !strings.HasPrefix(specURL, "http") {
					if u, err := url.Parse(sourceURL); err == nil {
						if strings.HasPrefix(specURL, "/") {
							specURL = fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, specURL)
						} else {
							specURL = fmt.Sprintf("%s://%s/%s", u.Scheme, u.Host, specURL)
						}
					}
				}
				sd.fetchAndParseSpec(specURL)
			}
		}
	}
}

// GetSpecs 获取所有规范
func (sd *SwaggerDiscoverer) GetSpecs() []*SwaggerSpec {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	specs := make([]*SwaggerSpec, 0, len(sd.specs))
	for _, spec := range sd.specs {
		specs = append(specs, spec)
	}
	return specs
}

// GetEndpoints 获取所有端点
func (sd *SwaggerDiscoverer) GetEndpoints() []*SwaggerEndpoint {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	endpoints := make([]*SwaggerEndpoint, len(sd.endpoints))
	copy(endpoints, sd.endpoints)
	return endpoints
}

// GetEndpointsByTag 按标签获取端点
func (sd *SwaggerDiscoverer) GetEndpointsByTag(tag string) []*SwaggerEndpoint {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	result := make([]*SwaggerEndpoint, 0)
	for _, ep := range sd.endpoints {
		for _, t := range ep.Tags {
			if t == tag {
				result = append(result, ep)
				break
			}
		}
	}
	return result
}

// GetEndpointsByMethod 按方法获取端点
func (sd *SwaggerDiscoverer) GetEndpointsByMethod(method string) []*SwaggerEndpoint {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	method = strings.ToUpper(method)
	result := make([]*SwaggerEndpoint, 0)
	for _, ep := range sd.endpoints {
		if ep.Method == method {
			result = append(result, ep)
		}
	}
	return result
}

// GetStats 获取统计信息
func (sd *SwaggerDiscoverer) GetStats() SwaggerStats {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	stats := SwaggerStats{
		SpecsDiscovered: len(sd.specs),
		SpecsParsed:     len(sd.specs),
		EndpointsFound:  len(sd.endpoints),
	}

	for _, spec := range sd.specs {
		if strings.HasPrefix(spec.Version, "2") {
			stats.Swagger2Count++
		} else if strings.HasPrefix(spec.Version, "3") {
			stats.OpenAPI3Count++
		}
		stats.PathsTotal += len(spec.Paths)
		stats.OperationsTotal += spec.EndpointCount
	}

	return stats
}

// GetResult 获取完整结果
func (sd *SwaggerDiscoverer) GetResult() *SwaggerResult {
	return &SwaggerResult{
		Specs:     sd.GetSpecs(),
		Endpoints: sd.GetEndpoints(),
		Stats:     sd.GetStats(),
	}
}

// SwaggerResult Swagger 发现结果
type SwaggerResult struct {
	Specs     []*SwaggerSpec   `json:"specs"`
	Endpoints []*SwaggerEndpoint   `json:"endpoints"`
	Stats     SwaggerStats     `json:"stats"`
}

// Clear 清空数据
func (sd *SwaggerDiscoverer) Clear() {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.specs = make(map[string]*SwaggerSpec)
	sd.endpoints = make([]*SwaggerEndpoint, 0)
}

// ExportEndpointsAsURLs 导出端点为 URL 列表
func (sd *SwaggerDiscoverer) ExportEndpointsAsURLs() []string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	urls := make([]string, 0, len(sd.endpoints))
	seen := make(map[string]bool)

	for _, ep := range sd.endpoints {
		// 使用示例请求的完整 URL
		var urlStr string
		if ep.SampleRequest != nil {
			urlStr = ep.SampleRequest.URL
		} else {
			urlStr = ep.URL
		}

		key := ep.Method + " " + urlStr
		if !seen[key] {
			seen[key] = true
			urls = append(urls, urlStr)
		}
	}

	sort.Strings(urls)
	return urls
}

// ExportEndpointsForCrawler 导出端点供爬虫使用
func (sd *SwaggerDiscoverer) ExportEndpointsForCrawler() []*UrlInfo {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	urlInfos := make([]*UrlInfo, 0, len(sd.endpoints))
	seen := make(map[string]bool)

	for _, ep := range sd.endpoints {
		var urlStr string
		if ep.SampleRequest != nil {
			urlStr = ep.SampleRequest.URL
		} else {
			urlStr = ep.URL
		}

		if !seen[urlStr] {
			seen[urlStr] = true
			urlInfos = append(urlInfos, &UrlInfo{
				Url:        urlStr,
				SourceType: "swagger:" + ep.Source,
				SourceUrl:  ep.SpecURL,
				Match:      ep.Method + " " + ep.Path,
			})
		}
	}

	return urlInfos
}

// countOperations 计算路径项中的操作数
func countOperations(item *PathItem) int {
	count := 0
	if item.Get != nil {
		count++
	}
	if item.Post != nil {
		count++
	}
	if item.Put != nil {
		count++
	}
	if item.Delete != nil {
		count++
	}
	if item.Patch != nil {
		count++
	}
	if item.Options != nil {
		count++
	}
	if item.Head != nil {
		count++
	}
	return count
}
