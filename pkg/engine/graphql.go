// Package engine provides GraphQL endpoint discovery and introspection
// P3优化: GraphQL 支持 - 自动发现端点、内省查询、schema 提取
package engine

import (
	"argo/pkg/log"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// GraphQLConfig GraphQL 发现配置
type GraphQLConfig struct {
	Enabled              bool          `yaml:"enabled" json:"enabled"`
	AutoDiscover         bool          `yaml:"auto_discover" json:"auto_discover"`                   // 自动发现端点
	EnableIntrospection  bool          `yaml:"enable_introspection" json:"enable_introspection"`     // 启用内省查询
	MaxDepth             int           `yaml:"max_depth" json:"max_depth"`                           // 内省最大深度
	Timeout              time.Duration `yaml:"timeout" json:"timeout"`                               // 请求超时
	ExtractQueries       bool          `yaml:"extract_queries" json:"extract_queries"`               // 提取查询
	GenerateSampleQueries bool         `yaml:"generate_sample_queries" json:"generate_sample_queries"` // 生成示例查询
}

// DefaultGraphQLConfig 返回默认配置
func DefaultGraphQLConfig() *GraphQLConfig {
	return &GraphQLConfig{
		Enabled:              true,
		AutoDiscover:         true,
		EnableIntrospection:  true,
		MaxDepth:             3,
		Timeout:              10 * time.Second,
		ExtractQueries:       true,
		GenerateSampleQueries: true,
	}
}

// GraphQLDiscoverer GraphQL 发现器
type GraphQLDiscoverer struct {
	config     *GraphQLConfig
	client     *http.Client
	mu         sync.RWMutex
	endpoints  map[string]*GraphQLEndpoint // key: URL
	schemas    map[string]*GraphQLSchema   // key: URL
	queries    []*ExtractedQuery
	engine     *EngineInfo
}

// GraphQLEndpoint GraphQL 端点信息
type GraphQLEndpoint struct {
	URL               string            `json:"url"`
	Method            string            `json:"method"`              // POST/GET
	DiscoveredAt      time.Time         `json:"discovered_at"`
	SupportsIntrospection bool          `json:"supports_introspection"`
	Headers           map[string]string `json:"headers,omitempty"`
	Source            string            `json:"source"`              // 发现来源
	Verified          bool              `json:"verified"`            // 是否验证过
	ResponseTime      time.Duration     `json:"response_time"`
}

// GraphQLSchema GraphQL Schema 信息
type GraphQLSchema struct {
	URL           string              `json:"url"`
	QueryType     *GraphQLType        `json:"query_type,omitempty"`
	MutationType  *GraphQLType        `json:"mutation_type,omitempty"`
	SubscriptionType *GraphQLType     `json:"subscription_type,omitempty"`
	Types         []*GraphQLType      `json:"types"`
	Directives    []*GraphQLDirective `json:"directives,omitempty"`
	IntrospectedAt time.Time          `json:"introspected_at"`
	Raw           map[string]interface{} `json:"raw,omitempty"`
}

// GraphQLType GraphQL 类型定义
type GraphQLType struct {
	Name        string           `json:"name"`
	Kind        string           `json:"kind"` // OBJECT, SCALAR, ENUM, INPUT_OBJECT, etc.
	Description string           `json:"description,omitempty"`
	Fields      []*GraphQLField  `json:"fields,omitempty"`
	InputFields []*GraphQLField  `json:"input_fields,omitempty"`
	EnumValues  []*GraphQLEnumValue `json:"enum_values,omitempty"`
	Interfaces  []string         `json:"interfaces,omitempty"`
	PossibleTypes []string       `json:"possible_types,omitempty"`
}

// GraphQLField GraphQL 字段定义
type GraphQLField struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Type        *GraphQLTypeRef `json:"type"`
	Args        []*GraphQLArg   `json:"args,omitempty"`
	IsDeprecated bool           `json:"is_deprecated"`
	DeprecationReason string    `json:"deprecation_reason,omitempty"`
}

// GraphQLTypeRef GraphQL 类型引用
type GraphQLTypeRef struct {
	Kind   string        `json:"kind"`
	Name   string        `json:"name,omitempty"`
	OfType *GraphQLTypeRef `json:"of_type,omitempty"`
}

// GraphQLArg GraphQL 参数定义
type GraphQLArg struct {
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Type         *GraphQLTypeRef `json:"type"`
	DefaultValue string        `json:"default_value,omitempty"`
}

// GraphQLEnumValue GraphQL 枚举值
type GraphQLEnumValue struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsDeprecated bool  `json:"is_deprecated"`
}

// GraphQLDirective GraphQL 指令
type GraphQLDirective struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Locations   []string `json:"locations"`
	Args        []*GraphQLArg `json:"args,omitempty"`
}

// ExtractedQuery 提取的查询
type ExtractedQuery struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"` // query, mutation, subscription
	Query     string   `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
	Source    string   `json:"source"` // introspection, js_analysis, etc.
	Endpoint  string   `json:"endpoint"`
}

// GraphQLStats GraphQL 统计
type GraphQLStats struct {
	EndpointsDiscovered int `json:"endpoints_discovered"`
	EndpointsVerified   int `json:"endpoints_verified"`
	SchemasIntrospected int `json:"schemas_introspected"`
	QueriesExtracted    int `json:"queries_extracted"`
	MutationsFound      int `json:"mutations_found"`
	TypesFound          int `json:"types_found"`
}

// 常见 GraphQL 端点路径
var commonGraphQLPaths = []string{
	"/graphql",
	"/gql",
	"/api/graphql",
	"/api/gql",
	"/v1/graphql",
	"/v2/graphql",
	"/query",
	"/api/query",
	"/graphql/v1",
	"/graphql/v2",
	"/_graphql",
	"/admin/graphql",
	"/console/graphql",
	"/playground",
	"/graphiql",
	"/altair",
	"/voyager",
}

// GraphQL 内省查询
const introspectionQuery = `
query IntrospectionQuery {
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types {
      kind
      name
      description
      fields(includeDeprecated: true) {
        name
        description
        args {
          name
          description
          type {
            kind
            name
            ofType {
              kind
              name
              ofType {
                kind
                name
                ofType {
                  kind
                  name
                }
              }
            }
          }
          defaultValue
        }
        type {
          kind
          name
          ofType {
            kind
            name
            ofType {
              kind
              name
              ofType {
                kind
                name
              }
            }
          }
        }
        isDeprecated
        deprecationReason
      }
      inputFields {
        name
        description
        type {
          kind
          name
          ofType {
            kind
            name
            ofType {
              kind
              name
            }
          }
        }
        defaultValue
      }
      interfaces {
        name
      }
      enumValues(includeDeprecated: true) {
        name
        description
        isDeprecated
        deprecationReason
      }
      possibleTypes {
        name
      }
    }
    directives {
      name
      description
      locations
      args {
        name
        description
        type {
          kind
          name
          ofType {
            kind
            name
          }
        }
        defaultValue
      }
    }
  }
}
`

// 简化版内省查询(用于检测)
const simpleIntrospectionQuery = `
query {
  __schema {
    queryType { name }
    mutationType { name }
  }
}
`

// JS 中的 GraphQL 模式
var graphqlPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)graphql\s*[:\(]`),
	regexp.MustCompile(`(?i)['"]query['"]:\s*['"]`),
	regexp.MustCompile(`(?i)['"]mutation['"]:\s*['"]`),
	regexp.MustCompile(`(?i)gql\s*\x60`), // gql`...`
	regexp.MustCompile(`(?i)query\s+\w+\s*[({]`),
	regexp.MustCompile(`(?i)mutation\s+\w+\s*[({]`),
	regexp.MustCompile(`(?i)subscription\s+\w+\s*[({]`),
	regexp.MustCompile(`(?i)__schema`),
	regexp.MustCompile(`(?i)__typename`),
}

// NewGraphQLDiscoverer 创建 GraphQL 发现器
func NewGraphQLDiscoverer(engine *EngineInfo, config *GraphQLConfig) *GraphQLDiscoverer {
	if config == nil {
		config = DefaultGraphQLConfig()
	}

	return &GraphQLDiscoverer{
		config:    config,
		client:    &http.Client{Timeout: config.Timeout},
		endpoints: make(map[string]*GraphQLEndpoint),
		schemas:   make(map[string]*GraphQLSchema),
		queries:   make([]*ExtractedQuery, 0),
		engine:    engine,
	}
}

// DiscoverFromBaseURL 从基础URL发现GraphQL端点
func (gd *GraphQLDiscoverer) DiscoverFromBaseURL(baseURL string) []*GraphQLEndpoint {
	if !gd.config.Enabled || !gd.config.AutoDiscover {
		return nil
	}

	discovered := make([]*GraphQLEndpoint, 0)

	u, err := url.Parse(baseURL)
	if err != nil {
		log.Logger.Debugf("graphql: invalid base URL: %s", baseURL)
		return nil
	}

	base := fmt.Sprintf("%s://%s", u.Scheme, u.Host)

	// 尝试常见路径
	for _, path := range commonGraphQLPaths {
		endpointURL := base + path
		if endpoint := gd.probeEndpoint(endpointURL); endpoint != nil {
			discovered = append(discovered, endpoint)
			log.Logger.Infof("graphql: discovered endpoint %s", endpointURL)
		}
	}

	return discovered
}

// probeEndpoint 探测端点是否为GraphQL
func (gd *GraphQLDiscoverer) probeEndpoint(endpointURL string) *GraphQLEndpoint {
	start := time.Now()

	// 发送简单的内省查询
	payload := map[string]interface{}{
		"query": simpleIntrospectionQuery,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := gd.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	responseTime := time.Since(start)

	// 检查响应是否为GraphQL格式
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil
	}

	// 检查是否有data或errors字段(GraphQL响应特征)
	_, hasData := result["data"]
	_, hasErrors := result["errors"]

	if !hasData && !hasErrors {
		return nil
	}

	endpoint := &GraphQLEndpoint{
		URL:          endpointURL,
		Method:       "POST",
		DiscoveredAt: time.Now(),
		Source:       "auto_discover",
		ResponseTime: responseTime,
		Verified:     true,
	}

	// 检查是否支持内省
	if hasData {
		if data, ok := result["data"].(map[string]interface{}); ok {
			if _, hasSchema := data["__schema"]; hasSchema {
				endpoint.SupportsIntrospection = true
			}
		}
	}

	gd.addEndpoint(endpoint)

	return endpoint
}

// addEndpoint 添加端点
func (gd *GraphQLDiscoverer) addEndpoint(endpoint *GraphQLEndpoint) {
	gd.mu.Lock()
	defer gd.mu.Unlock()

	if _, exists := gd.endpoints[endpoint.URL]; !exists {
		gd.endpoints[endpoint.URL] = endpoint
	}
}

// AnalyzeContent 分析内容中的GraphQL信息
func (gd *GraphQLDiscoverer) AnalyzeContent(content string, sourceURL string) {
	if !gd.config.Enabled {
		return
	}

	// 检测GraphQL模式
	for _, pattern := range graphqlPatterns {
		if pattern.MatchString(content) {
			log.Logger.Debugf("graphql: detected pattern in %s", sourceURL)
			gd.extractQueriesFromContent(content, sourceURL)
			break
		}
	}

	// 提取可能的GraphQL端点URL
	gd.extractEndpointURLs(content, sourceURL)
}

// extractQueriesFromContent 从内容中提取GraphQL查询
func (gd *GraphQLDiscoverer) extractQueriesFromContent(content string, sourceURL string) {
	// 匹配 gql`...` 模板字符串
	gqlTemplatePattern := regexp.MustCompile("(?s)gql\\s*`([^`]+)`")
	matches := gqlTemplatePattern.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) > 1 {
			query := strings.TrimSpace(match[1])
			if gd.isValidGraphQLQuery(query) {
				gd.addExtractedQuery(&ExtractedQuery{
					Query:  query,
					Type:   gd.detectQueryType(query),
					Source: "js_template",
					Endpoint: sourceURL,
				})
			}
		}
	}

	// 匹配 query/mutation 定义
	queryDefPattern := regexp.MustCompile(`(?s)(query|mutation|subscription)\s+(\w+)\s*(\([^)]*\))?\s*\{[^}]+\}`)
	matches = queryDefPattern.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) > 0 {
			query := strings.TrimSpace(match[0])
			queryType := "query"
			queryName := ""
			if len(match) > 1 {
				queryType = strings.ToLower(match[1])
			}
			if len(match) > 2 {
				queryName = match[2]
			}
			gd.addExtractedQuery(&ExtractedQuery{
				Name:   queryName,
				Query:  query,
				Type:   queryType,
				Source: "js_definition",
				Endpoint: sourceURL,
			})
		}
	}

	// 匹配JSON中的query字段
	jsonQueryPattern := regexp.MustCompile(`(?s)["']query["']\s*:\s*["']([^"']+)["']`)
	matches = jsonQueryPattern.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) > 1 {
			query := strings.TrimSpace(match[1])
			// 处理转义字符
			query = strings.ReplaceAll(query, "\\n", "\n")
			query = strings.ReplaceAll(query, "\\t", "\t")
			if gd.isValidGraphQLQuery(query) {
				gd.addExtractedQuery(&ExtractedQuery{
					Query:  query,
					Type:   gd.detectQueryType(query),
					Source: "json_field",
					Endpoint: sourceURL,
				})
			}
		}
	}
}

// extractEndpointURLs 从内容中提取端点URL
func (gd *GraphQLDiscoverer) extractEndpointURLs(content string, sourceURL string) {
	// 匹配包含graphql的URL
	urlPattern := regexp.MustCompile(`(?i)["'](https?://[^"'\s]+(?:graphql|gql)[^"'\s]*)["']`)
	matches := urlPattern.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) > 1 {
			endpointURL := match[1]
			gd.addEndpoint(&GraphQLEndpoint{
				URL:          endpointURL,
				Method:       "POST",
				DiscoveredAt: time.Now(),
				Source:       "js_analysis",
				Verified:     false,
			})
		}
	}

	// 匹配路径
	pathPattern := regexp.MustCompile(`(?i)["'](/[^"'\s]*(?:graphql|gql)[^"'\s]*)["']`)
	matches = pathPattern.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) > 1 {
			path := match[1]
			// 构建完整URL
			if u, err := url.Parse(sourceURL); err == nil {
				endpointURL := fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, path)
				gd.addEndpoint(&GraphQLEndpoint{
					URL:          endpointURL,
					Method:       "POST",
					DiscoveredAt: time.Now(),
					Source:       "js_path",
					Verified:     false,
				})
			}
		}
	}
}

// isValidGraphQLQuery 验证是否为有效的GraphQL查询
func (gd *GraphQLDiscoverer) isValidGraphQLQuery(query string) bool {
	query = strings.TrimSpace(query)
	if len(query) < 10 {
		return false
	}

	// 检查基本结构
	hasQueryKeyword := strings.Contains(strings.ToLower(query), "query") ||
		strings.Contains(strings.ToLower(query), "mutation") ||
		strings.Contains(strings.ToLower(query), "subscription")

	hasBraces := strings.Contains(query, "{") && strings.Contains(query, "}")

	return hasQueryKeyword || hasBraces
}

// detectQueryType 检测查询类型
func (gd *GraphQLDiscoverer) detectQueryType(query string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	if strings.HasPrefix(query, "mutation") {
		return "mutation"
	}
	if strings.HasPrefix(query, "subscription") {
		return "subscription"
	}
	return "query"
}

// addExtractedQuery 添加提取的查询
func (gd *GraphQLDiscoverer) addExtractedQuery(query *ExtractedQuery) {
	gd.mu.Lock()
	defer gd.mu.Unlock()

	// 去重
	for _, existing := range gd.queries {
		if existing.Query == query.Query {
			return
		}
	}

	gd.queries = append(gd.queries, query)
}

// Introspect 对端点执行内省查询
func (gd *GraphQLDiscoverer) Introspect(endpointURL string) (*GraphQLSchema, error) {
	if !gd.config.Enabled || !gd.config.EnableIntrospection {
		return nil, fmt.Errorf("introspection disabled")
	}

	payload := map[string]interface{}{
		"query": introspectionQuery,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := gd.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result struct {
		Data struct {
			Schema map[string]interface{} `json:"__schema"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("graphql error: %s", result.Errors[0].Message)
	}

	if result.Data.Schema == nil {
		return nil, fmt.Errorf("no schema in response")
	}

	schema := gd.parseSchema(result.Data.Schema, endpointURL)

	gd.mu.Lock()
	gd.schemas[endpointURL] = schema
	gd.mu.Unlock()

	log.Logger.Infof("graphql: introspected %s, found %d types", endpointURL, len(schema.Types))

	// 生成示例查询
	if gd.config.GenerateSampleQueries {
		gd.generateSampleQueries(schema, endpointURL)
	}

	return schema, nil
}

// parseSchema 解析Schema
func (gd *GraphQLDiscoverer) parseSchema(raw map[string]interface{}, endpointURL string) *GraphQLSchema {
	schema := &GraphQLSchema{
		URL:           endpointURL,
		IntrospectedAt: time.Now(),
		Raw:           raw,
		Types:         make([]*GraphQLType, 0),
	}

	// 解析queryType
	if qt, ok := raw["queryType"].(map[string]interface{}); ok {
		if name, ok := qt["name"].(string); ok {
			schema.QueryType = &GraphQLType{Name: name}
		}
	}

	// 解析mutationType
	if mt, ok := raw["mutationType"].(map[string]interface{}); ok {
		if name, ok := mt["name"].(string); ok {
			schema.MutationType = &GraphQLType{Name: name}
		}
	}

	// 解析subscriptionType
	if st, ok := raw["subscriptionType"].(map[string]interface{}); ok {
		if name, ok := st["name"].(string); ok {
			schema.SubscriptionType = &GraphQLType{Name: name}
		}
	}

	// 解析types
	if types, ok := raw["types"].([]interface{}); ok {
		for _, t := range types {
			if typeMap, ok := t.(map[string]interface{}); ok {
				graphQLType := gd.parseType(typeMap)
				if graphQLType != nil && !strings.HasPrefix(graphQLType.Name, "__") {
					schema.Types = append(schema.Types, graphQLType)
				}
			}
		}
	}

	return schema
}

// parseType 解析类型
func (gd *GraphQLDiscoverer) parseType(typeMap map[string]interface{}) *GraphQLType {
	t := &GraphQLType{
		Fields:      make([]*GraphQLField, 0),
		InputFields: make([]*GraphQLField, 0),
		EnumValues:  make([]*GraphQLEnumValue, 0),
	}

	if name, ok := typeMap["name"].(string); ok {
		t.Name = name
	}
	if kind, ok := typeMap["kind"].(string); ok {
		t.Kind = kind
	}
	if desc, ok := typeMap["description"].(string); ok {
		t.Description = desc
	}

	// 解析fields
	if fields, ok := typeMap["fields"].([]interface{}); ok {
		for _, f := range fields {
			if fieldMap, ok := f.(map[string]interface{}); ok {
				field := gd.parseField(fieldMap)
				if field != nil {
					t.Fields = append(t.Fields, field)
				}
			}
		}
	}

	// 解析inputFields
	if inputFields, ok := typeMap["inputFields"].([]interface{}); ok {
		for _, f := range inputFields {
			if fieldMap, ok := f.(map[string]interface{}); ok {
				field := gd.parseField(fieldMap)
				if field != nil {
					t.InputFields = append(t.InputFields, field)
				}
			}
		}
	}

	// 解析enumValues
	if enumValues, ok := typeMap["enumValues"].([]interface{}); ok {
		for _, ev := range enumValues {
			if evMap, ok := ev.(map[string]interface{}); ok {
				enumVal := &GraphQLEnumValue{}
				if name, ok := evMap["name"].(string); ok {
					enumVal.Name = name
				}
				if desc, ok := evMap["description"].(string); ok {
					enumVal.Description = desc
				}
				if deprecated, ok := evMap["isDeprecated"].(bool); ok {
					enumVal.IsDeprecated = deprecated
				}
				t.EnumValues = append(t.EnumValues, enumVal)
			}
		}
	}

	// 解析interfaces
	if interfaces, ok := typeMap["interfaces"].([]interface{}); ok {
		for _, i := range interfaces {
			if iMap, ok := i.(map[string]interface{}); ok {
				if name, ok := iMap["name"].(string); ok {
					t.Interfaces = append(t.Interfaces, name)
				}
			}
		}
	}

	return t
}

// parseField 解析字段
func (gd *GraphQLDiscoverer) parseField(fieldMap map[string]interface{}) *GraphQLField {
	field := &GraphQLField{
		Args: make([]*GraphQLArg, 0),
	}

	if name, ok := fieldMap["name"].(string); ok {
		field.Name = name
	}
	if desc, ok := fieldMap["description"].(string); ok {
		field.Description = desc
	}
	if deprecated, ok := fieldMap["isDeprecated"].(bool); ok {
		field.IsDeprecated = deprecated
	}
	if reason, ok := fieldMap["deprecationReason"].(string); ok {
		field.DeprecationReason = reason
	}

	// 解析type
	if typeRef, ok := fieldMap["type"].(map[string]interface{}); ok {
		field.Type = gd.parseTypeRef(typeRef)
	}

	// 解析args
	if args, ok := fieldMap["args"].([]interface{}); ok {
		for _, a := range args {
			if argMap, ok := a.(map[string]interface{}); ok {
				arg := gd.parseArg(argMap)
				if arg != nil {
					field.Args = append(field.Args, arg)
				}
			}
		}
	}

	return field
}

// parseTypeRef 解析类型引用
func (gd *GraphQLDiscoverer) parseTypeRef(typeMap map[string]interface{}) *GraphQLTypeRef {
	ref := &GraphQLTypeRef{}

	if kind, ok := typeMap["kind"].(string); ok {
		ref.Kind = kind
	}
	if name, ok := typeMap["name"].(string); ok {
		ref.Name = name
	}
	if ofType, ok := typeMap["ofType"].(map[string]interface{}); ok {
		ref.OfType = gd.parseTypeRef(ofType)
	}

	return ref
}

// parseArg 解析参数
func (gd *GraphQLDiscoverer) parseArg(argMap map[string]interface{}) *GraphQLArg {
	arg := &GraphQLArg{}

	if name, ok := argMap["name"].(string); ok {
		arg.Name = name
	}
	if desc, ok := argMap["description"].(string); ok {
		arg.Description = desc
	}
	if defaultVal, ok := argMap["defaultValue"].(string); ok {
		arg.DefaultValue = defaultVal
	}
	if typeRef, ok := argMap["type"].(map[string]interface{}); ok {
		arg.Type = gd.parseTypeRef(typeRef)
	}

	return arg
}

// generateSampleQueries 生成示例查询
func (gd *GraphQLDiscoverer) generateSampleQueries(schema *GraphQLSchema, endpointURL string) {
	// 为Query类型生成查询
	if schema.QueryType != nil {
		for _, t := range schema.Types {
			if t.Name == schema.QueryType.Name && t.Kind == "OBJECT" {
				for _, field := range t.Fields {
					query := gd.buildSampleQuery("query", field)
					gd.addExtractedQuery(&ExtractedQuery{
						Name:     field.Name,
						Type:     "query",
						Query:    query,
						Source:   "introspection",
						Endpoint: endpointURL,
					})
				}
				break
			}
		}
	}

	// 为Mutation类型生成查询
	if schema.MutationType != nil {
		for _, t := range schema.Types {
			if t.Name == schema.MutationType.Name && t.Kind == "OBJECT" {
				for _, field := range t.Fields {
					query := gd.buildSampleQuery("mutation", field)
					gd.addExtractedQuery(&ExtractedQuery{
						Name:     field.Name,
						Type:     "mutation",
						Query:    query,
						Source:   "introspection",
						Endpoint: endpointURL,
					})
				}
				break
			}
		}
	}
}

// buildSampleQuery 构建示例查询
func (gd *GraphQLDiscoverer) buildSampleQuery(queryType string, field *GraphQLField) string {
	var sb strings.Builder

	sb.WriteString(queryType)
	sb.WriteString(" ")
	sb.WriteString(strings.Title(field.Name))

	// 添加参数
	if len(field.Args) > 0 {
		sb.WriteString("(")
		params := make([]string, 0)
		for _, arg := range field.Args {
			typeName := gd.getTypeName(arg.Type)
			params = append(params, fmt.Sprintf("$%s: %s", arg.Name, typeName))
		}
		sb.WriteString(strings.Join(params, ", "))
		sb.WriteString(")")
	}

	sb.WriteString(" {\n  ")
	sb.WriteString(field.Name)

	// 添加参数传递
	if len(field.Args) > 0 {
		sb.WriteString("(")
		args := make([]string, 0)
		for _, arg := range field.Args {
			args = append(args, fmt.Sprintf("%s: $%s", arg.Name, arg.Name))
		}
		sb.WriteString(strings.Join(args, ", "))
		sb.WriteString(")")
	}

	// 简单的字段选择
	sb.WriteString(" {\n    __typename\n  }\n}")

	return sb.String()
}

// getTypeName 获取类型名称
func (gd *GraphQLDiscoverer) getTypeName(typeRef *GraphQLTypeRef) string {
	if typeRef == nil {
		return "String"
	}

	switch typeRef.Kind {
	case "NON_NULL":
		return gd.getTypeName(typeRef.OfType) + "!"
	case "LIST":
		return "[" + gd.getTypeName(typeRef.OfType) + "]"
	default:
		if typeRef.Name != "" {
			return typeRef.Name
		}
		return "String"
	}
}

// IntrospectAll 内省所有发现的端点
func (gd *GraphQLDiscoverer) IntrospectAll() {
	gd.mu.RLock()
	endpoints := make([]*GraphQLEndpoint, 0, len(gd.endpoints))
	for _, ep := range gd.endpoints {
		endpoints = append(endpoints, ep)
	}
	gd.mu.RUnlock()

	for _, ep := range endpoints {
		if ep.SupportsIntrospection || !ep.Verified {
			if _, err := gd.Introspect(ep.URL); err != nil {
				log.Logger.Debugf("graphql: introspection failed for %s: %v", ep.URL, err)
			}
		}
	}
}

// VerifyEndpoints 验证所有未验证的端点
func (gd *GraphQLDiscoverer) VerifyEndpoints() {
	gd.mu.RLock()
	endpoints := make([]*GraphQLEndpoint, 0)
	for _, ep := range gd.endpoints {
		if !ep.Verified {
			endpoints = append(endpoints, ep)
		}
	}
	gd.mu.RUnlock()

	for _, ep := range endpoints {
		if verified := gd.probeEndpoint(ep.URL); verified != nil {
			gd.mu.Lock()
			gd.endpoints[ep.URL] = verified
			gd.mu.Unlock()
		}
	}
}

// GetEndpoints 获取所有端点
func (gd *GraphQLDiscoverer) GetEndpoints() []*GraphQLEndpoint {
	gd.mu.RLock()
	defer gd.mu.RUnlock()

	endpoints := make([]*GraphQLEndpoint, 0, len(gd.endpoints))
	for _, ep := range gd.endpoints {
		endpoints = append(endpoints, ep)
	}
	return endpoints
}

// GetSchemas 获取所有Schema
func (gd *GraphQLDiscoverer) GetSchemas() []*GraphQLSchema {
	gd.mu.RLock()
	defer gd.mu.RUnlock()

	schemas := make([]*GraphQLSchema, 0, len(gd.schemas))
	for _, s := range gd.schemas {
		schemas = append(schemas, s)
	}
	return schemas
}

// GetQueries 获取所有提取的查询
func (gd *GraphQLDiscoverer) GetQueries() []*ExtractedQuery {
	gd.mu.RLock()
	defer gd.mu.RUnlock()

	queries := make([]*ExtractedQuery, len(gd.queries))
	copy(queries, gd.queries)
	return queries
}

// GetStats 获取统计信息
func (gd *GraphQLDiscoverer) GetStats() GraphQLStats {
	gd.mu.RLock()
	defer gd.mu.RUnlock()

	stats := GraphQLStats{
		EndpointsDiscovered: len(gd.endpoints),
		SchemasIntrospected: len(gd.schemas),
		QueriesExtracted:    len(gd.queries),
	}

	for _, ep := range gd.endpoints {
		if ep.Verified {
			stats.EndpointsVerified++
		}
	}

	for _, q := range gd.queries {
		if q.Type == "mutation" {
			stats.MutationsFound++
		}
	}

	for _, s := range gd.schemas {
		stats.TypesFound += len(s.Types)
	}

	return stats
}

// GetResult 获取完整结果
func (gd *GraphQLDiscoverer) GetResult() *GraphQLResult {
	return &GraphQLResult{
		Endpoints: gd.GetEndpoints(),
		Schemas:   gd.GetSchemas(),
		Queries:   gd.GetQueries(),
		Stats:     gd.GetStats(),
	}
}

// GraphQLResult GraphQL 发现结果
type GraphQLResult struct {
	Endpoints []*GraphQLEndpoint `json:"endpoints"`
	Schemas   []*GraphQLSchema   `json:"schemas"`
	Queries   []*ExtractedQuery  `json:"queries"`
	Stats     GraphQLStats       `json:"stats"`
}

// Clear 清空数据
func (gd *GraphQLDiscoverer) Clear() {
	gd.mu.Lock()
	defer gd.mu.Unlock()

	gd.endpoints = make(map[string]*GraphQLEndpoint)
	gd.schemas = make(map[string]*GraphQLSchema)
	gd.queries = make([]*ExtractedQuery, 0)
}
