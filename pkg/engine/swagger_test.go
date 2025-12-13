package engine

import (
	"argo/pkg/log"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func init() {
	// 初始化日志以避免测试中的 nil pointer
	log.Init(false, true) // quiet mode
}

func TestSwaggerConfig_Default(t *testing.T) {
	config := DefaultSwaggerConfig()

	if !config.Enabled {
		t.Error("Expected Enabled to be true by default")
	}
	if !config.AutoDiscover {
		t.Error("Expected AutoDiscover to be true by default")
	}
	if !config.ParseSpec {
		t.Error("Expected ParseSpec to be true by default")
	}
	if !config.GenerateRequests {
		t.Error("Expected GenerateRequests to be true by default")
	}
	if config.Timeout != 15*time.Second {
		t.Errorf("Expected Timeout=15s, got %v", config.Timeout)
	}
	if !config.FollowBasePath {
		t.Error("Expected FollowBasePath to be true by default")
	}
	if !config.ExtractFromHTML {
		t.Error("Expected ExtractFromHTML to be true by default")
	}
}

func TestSwaggerDiscoverer_Basic(t *testing.T) {
	config := &SwaggerConfig{
		Enabled:      true,
		AutoDiscover: true,
		ParseSpec:    true,
		Timeout:      5 * time.Second,
	}

	discoverer := NewSwaggerDiscoverer(nil, config)
	if discoverer == nil {
		t.Fatal("NewSwaggerDiscoverer returned nil")
	}

	// 验证初始状态
	specs := discoverer.GetSpecs()
	if len(specs) != 0 {
		t.Errorf("Expected 0 specs initially, got %d", len(specs))
	}

	endpoints := discoverer.GetEndpoints()
	if len(endpoints) != 0 {
		t.Errorf("Expected 0 endpoints initially, got %d", len(endpoints))
	}
}

func TestSwaggerDiscoverer_ParseSwagger2(t *testing.T) {
	// 创建模拟 Swagger 2.0 服务器
	swagger2Spec := map[string]interface{}{
		"swagger": "2.0",
		"info": map[string]interface{}{
			"title":       "Test API",
			"description": "A test API",
			"version":     "1.0.0",
		},
		"host":     "api.example.com",
		"basePath": "/v1",
		"schemes":  []string{"https"},
		"paths": map[string]interface{}{
			"/users": map[string]interface{}{
				"get": map[string]interface{}{
					"operationId": "getUsers",
					"summary":     "Get all users",
					"tags":        []string{"users"},
					"produces":    []string{"application/json"},
					"parameters": []map[string]interface{}{
						{
							"name":     "limit",
							"in":       "query",
							"type":     "integer",
							"required": false,
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Success",
						},
					},
				},
				"post": map[string]interface{}{
					"operationId": "createUser",
					"summary":     "Create a user",
					"tags":        []string{"users"},
					"consumes":    []string{"application/json"},
					"parameters": []map[string]interface{}{
						{
							"name":     "body",
							"in":       "body",
							"required": true,
							"schema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"name":  map[string]interface{}{"type": "string"},
									"email": map[string]interface{}{"type": "string"},
								},
							},
						},
					},
				},
			},
			"/users/{id}": map[string]interface{}{
				"get": map[string]interface{}{
					"operationId": "getUserById",
					"summary":     "Get user by ID",
					"parameters": []map[string]interface{}{
						{
							"name":     "id",
							"in":       "path",
							"type":     "integer",
							"required": true,
						},
					},
				},
				"delete": map[string]interface{}{
					"operationId": "deleteUser",
					"summary":     "Delete a user",
					"deprecated":  true,
				},
			},
		},
		"tags": []map[string]interface{}{
			{"name": "users", "description": "User operations"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(swagger2Spec)
	}))
	defer server.Close()

	config := &SwaggerConfig{
		Enabled:          true,
		AutoDiscover:     true,
		ParseSpec:        true,
		GenerateRequests: true,
		Timeout:          5 * time.Second,
		FollowBasePath:   true,
	}

	discoverer := NewSwaggerDiscoverer(nil, config)

	// 手动获取并解析
	spec := discoverer.fetchAndParseSpec(server.URL)
	if spec == nil {
		t.Fatal("Failed to parse Swagger 2.0 spec")
	}

	if spec.Version != "2.0" {
		t.Errorf("Expected version='2.0', got '%s'", spec.Version)
	}
	if spec.Title != "Test API" {
		t.Errorf("Expected title='Test API', got '%s'", spec.Title)
	}
	if spec.Host != "api.example.com" {
		t.Errorf("Expected host='api.example.com', got '%s'", spec.Host)
	}
	if spec.BasePath != "/v1" {
		t.Errorf("Expected basePath='/v1', got '%s'", spec.BasePath)
	}

	endpoints := discoverer.GetEndpoints()
	if len(endpoints) != 4 { // GET /users, POST /users, GET /users/{id}, DELETE /users/{id}
		t.Errorf("Expected 4 endpoints, got %d", len(endpoints))
	}
}

func TestSwaggerDiscoverer_ParseOpenAPI3(t *testing.T) {
	// 创建模拟 OpenAPI 3.0 服务器
	openapi3Spec := map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":   "OpenAPI 3 Test",
			"version": "2.0.0",
		},
		"servers": []map[string]interface{}{
			{"url": "https://api.example.com/v2"},
		},
		"paths": map[string]interface{}{
			"/products": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List products",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Success",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "array",
										"items": map[string]interface{}{
											"$ref": "#/components/schemas/Product",
										},
									},
								},
							},
						},
					},
				},
				"post": map[string]interface{}{
					"summary": "Create product",
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"$ref": "#/components/schemas/CreateProduct",
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"Product": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":   map[string]interface{}{"type": "integer"},
						"name": map[string]interface{}{"type": "string"},
					},
				},
			},
			"securitySchemes": map[string]interface{}{
				"bearerAuth": map[string]interface{}{
					"type":   "http",
					"scheme": "bearer",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openapi3Spec)
	}))
	defer server.Close()

	config := DefaultSwaggerConfig()
	config.Timeout = 5 * time.Second

	discoverer := NewSwaggerDiscoverer(nil, config)
	spec := discoverer.fetchAndParseSpec(server.URL)

	if spec == nil {
		t.Fatal("Failed to parse OpenAPI 3.0 spec")
	}

	if spec.Version != "3.0.3" {
		t.Errorf("Expected version='3.0.3', got '%s'", spec.Version)
	}
	if spec.Title != "OpenAPI 3 Test" {
		t.Errorf("Expected title='OpenAPI 3 Test', got '%s'", spec.Title)
	}

	endpoints := discoverer.GetEndpoints()
	if len(endpoints) != 2 { // GET /products, POST /products
		t.Errorf("Expected 2 endpoints, got %d", len(endpoints))
	}
}

func TestSwaggerDiscoverer_Stats(t *testing.T) {
	config := DefaultSwaggerConfig()
	discoverer := NewSwaggerDiscoverer(nil, config)

	// 手动添加数据模拟发现结果
	discoverer.specs["http://example.com/swagger.json"] = &SwaggerSpec{
		URL:           "http://example.com/swagger.json",
		Version:       "2.0",
		Title:         "Test API",
		EndpointCount: 5,
		Paths: map[string]*PathItem{
			"/users":     {},
			"/products":  {},
			"/orders":    {},
		},
	}
	discoverer.specs["http://example.com/openapi.json"] = &SwaggerSpec{
		URL:           "http://example.com/openapi.json",
		Version:       "3.0.0",
		Title:         "OpenAPI Test",
		EndpointCount: 3,
		Paths: map[string]*PathItem{
			"/items": {},
		},
	}

	stats := discoverer.GetStats()

	if stats.SpecsDiscovered != 2 {
		t.Errorf("Expected SpecsDiscovered=2, got %d", stats.SpecsDiscovered)
	}
	if stats.Swagger2Count != 1 {
		t.Errorf("Expected Swagger2Count=1, got %d", stats.Swagger2Count)
	}
	if stats.OpenAPI3Count != 1 {
		t.Errorf("Expected OpenAPI3Count=1, got %d", stats.OpenAPI3Count)
	}
	if stats.PathsTotal != 4 {
		t.Errorf("Expected PathsTotal=4, got %d", stats.PathsTotal)
	}
}

func TestSwaggerDiscoverer_Clear(t *testing.T) {
	config := DefaultSwaggerConfig()
	discoverer := NewSwaggerDiscoverer(nil, config)

	// 添加数据
	discoverer.specs["http://example.com/swagger.json"] = &SwaggerSpec{
		URL:     "http://example.com/swagger.json",
		Version: "2.0",
	}
	discoverer.endpoints = append(discoverer.endpoints, &SwaggerEndpoint{
		URL:    "http://example.com/users",
		Method: "GET",
	})

	// 清空
	discoverer.Clear()

	if len(discoverer.GetSpecs()) != 0 {
		t.Error("Expected 0 specs after Clear")
	}
	if len(discoverer.GetEndpoints()) != 0 {
		t.Error("Expected 0 endpoints after Clear")
	}
}

func TestSwaggerDiscoverer_GetEndpointsByTag(t *testing.T) {
	config := DefaultSwaggerConfig()
	discoverer := NewSwaggerDiscoverer(nil, config)

	// 添加测试端点
	discoverer.endpoints = []*SwaggerEndpoint{
		{URL: "/users", Method: "GET", Tags: []string{"users", "public"}},
		{URL: "/users", Method: "POST", Tags: []string{"users", "admin"}},
		{URL: "/products", Method: "GET", Tags: []string{"products"}},
	}

	userEndpoints := discoverer.GetEndpointsByTag("users")
	if len(userEndpoints) != 2 {
		t.Errorf("Expected 2 endpoints with 'users' tag, got %d", len(userEndpoints))
	}

	productEndpoints := discoverer.GetEndpointsByTag("products")
	if len(productEndpoints) != 1 {
		t.Errorf("Expected 1 endpoint with 'products' tag, got %d", len(productEndpoints))
	}

	unknownEndpoints := discoverer.GetEndpointsByTag("unknown")
	if len(unknownEndpoints) != 0 {
		t.Errorf("Expected 0 endpoints with 'unknown' tag, got %d", len(unknownEndpoints))
	}
}

func TestSwaggerDiscoverer_GetEndpointsByMethod(t *testing.T) {
	config := DefaultSwaggerConfig()
	discoverer := NewSwaggerDiscoverer(nil, config)

	// 添加测试端点
	discoverer.endpoints = []*SwaggerEndpoint{
		{URL: "/users", Method: "GET"},
		{URL: "/products", Method: "GET"},
		{URL: "/users", Method: "POST"},
		{URL: "/users/{id}", Method: "DELETE"},
	}

	getEndpoints := discoverer.GetEndpointsByMethod("GET")
	if len(getEndpoints) != 2 {
		t.Errorf("Expected 2 GET endpoints, got %d", len(getEndpoints))
	}

	postEndpoints := discoverer.GetEndpointsByMethod("POST")
	if len(postEndpoints) != 1 {
		t.Errorf("Expected 1 POST endpoint, got %d", len(postEndpoints))
	}

	// 测试小写输入
	deleteEndpoints := discoverer.GetEndpointsByMethod("delete")
	if len(deleteEndpoints) != 1 {
		t.Errorf("Expected 1 DELETE endpoint, got %d", len(deleteEndpoints))
	}
}

func TestSwaggerDiscoverer_ExportEndpointsAsURLs(t *testing.T) {
	config := DefaultSwaggerConfig()
	discoverer := NewSwaggerDiscoverer(nil, config)

	// 添加测试端点
	discoverer.endpoints = []*SwaggerEndpoint{
		{URL: "http://example.com/users", Method: "GET"},
		{URL: "http://example.com/users", Method: "POST"},
		{URL: "http://example.com/products", Method: "GET"},
	}

	urls := discoverer.ExportEndpointsAsURLs()

	// ExportEndpointsAsURLs 按 Method+URL 去重，所以3个不同的组合都会保留
	if len(urls) != 3 {
		t.Errorf("Expected 3 unique URLs, got %d", len(urls))
	}
}

func TestSwaggerDiscoverer_ExportEndpointsForCrawler(t *testing.T) {
	config := DefaultSwaggerConfig()
	discoverer := NewSwaggerDiscoverer(nil, config)

	// 添加测试端点
	discoverer.endpoints = []*SwaggerEndpoint{
		{
			URL:     "http://example.com/users",
			Method:  "GET",
			Path:    "/users",
			Source:  "swagger_2.0",
			SpecURL: "http://example.com/swagger.json",
		},
	}

	urlInfos := discoverer.ExportEndpointsForCrawler()

	if len(urlInfos) != 1 {
		t.Errorf("Expected 1 UrlInfo, got %d", len(urlInfos))
	}

	if urlInfos[0].Url != "http://example.com/users" {
		t.Errorf("Expected URL='http://example.com/users', got '%s'", urlInfos[0].Url)
	}
	if urlInfos[0].SourceType != "swagger:swagger_2.0" {
		t.Errorf("Expected SourceType='swagger:swagger_2.0', got '%s'", urlInfos[0].SourceType)
	}
}

func TestSwaggerDiscoverer_GetResult(t *testing.T) {
	config := DefaultSwaggerConfig()
	discoverer := NewSwaggerDiscoverer(nil, config)

	// 添加数据
	discoverer.specs["http://example.com/swagger.json"] = &SwaggerSpec{
		URL:     "http://example.com/swagger.json",
		Version: "2.0",
		Title:   "Test",
	}
	discoverer.endpoints = []*SwaggerEndpoint{
		{URL: "http://example.com/users", Method: "GET"},
	}

	result := discoverer.GetResult()

	if result == nil {
		t.Fatal("GetResult returned nil")
	}
	if len(result.Specs) != 1 {
		t.Errorf("Expected 1 spec in result, got %d", len(result.Specs))
	}
	if len(result.Endpoints) != 1 {
		t.Errorf("Expected 1 endpoint in result, got %d", len(result.Endpoints))
	}
}

func TestSwaggerDiscoverer_Disabled(t *testing.T) {
	config := &SwaggerConfig{
		Enabled:      false,
		AutoDiscover: false,
	}

	discoverer := NewSwaggerDiscoverer(nil, config)

	// 禁用时应该返回 nil
	specs := discoverer.DiscoverFromBaseURL("http://example.com")
	if specs != nil {
		t.Error("Expected nil specs when disabled")
	}

	// 禁用时 AnalyzeContent 应该不执行任何操作
	discoverer.AnalyzeContent(`url: "http://example.com/swagger.json"`, "http://example.com")
	if len(discoverer.GetSpecs()) != 0 {
		t.Error("Expected no specs when disabled")
	}
}

func TestSwaggerDiscoverer_GenerateSampleRequest(t *testing.T) {
	config := &SwaggerConfig{
		Enabled:          true,
		GenerateRequests: true,
		Timeout:          5 * time.Second,
	}

	discoverer := NewSwaggerDiscoverer(nil, config)

	endpoint := &SwaggerEndpoint{
		URL:         "http://example.com/users/{id}",
		Method:      "POST",
		ContentType: "application/json",
		Parameters: []*Parameter{
			{Name: "id", In: "path", Type: "integer", Required: true},
			{Name: "limit", In: "query", Type: "integer", Default: 10},
			{Name: "Authorization", In: "header", Type: "string"},
		},
	}

	op := &Operation{
		Parameters: endpoint.Parameters,
	}

	sample := discoverer.generateSampleRequest(endpoint, op)

	if sample == nil {
		t.Fatal("generateSampleRequest returned nil")
	}
	if sample.Method != "POST" {
		t.Errorf("Expected method='POST', got '%s'", sample.Method)
	}
	if sample.ContentType != "application/json" {
		t.Errorf("Expected ContentType='application/json', got '%s'", sample.ContentType)
	}
	if sample.Headers["Authorization"] == "" {
		t.Error("Expected Authorization header in sample")
	}
}

func TestSwaggerDiscoverer_GetSampleValue(t *testing.T) {
	config := DefaultSwaggerConfig()
	discoverer := NewSwaggerDiscoverer(nil, config)

	tests := []struct {
		param    *Parameter
		expected interface{}
	}{
		{&Parameter{Name: "id", Type: "integer"}, 1},
		{&Parameter{Name: "active", Type: "boolean"}, true},
		{&Parameter{Name: "score", Type: "number"}, 1.0},
		{&Parameter{Name: "status", Type: "string", Enum: []interface{}{"active", "inactive"}}, "active"},
		{&Parameter{Name: "count", Type: "integer", Default: 100}, 100},
		{&Parameter{Name: "email", Type: "string"}, "test@example.com"},
		{&Parameter{Name: "page", Type: "string"}, "1"},
	}

	for _, tt := range tests {
		result := discoverer.getSampleValue(tt.param)
		if result != tt.expected {
			t.Errorf("getSampleValue(%s) = %v, want %v", tt.param.Name, result, tt.expected)
		}
	}
}

func TestCommonSwaggerPaths(t *testing.T) {
	// 确保常见路径列表包含预期的路径
	expectedPaths := []string{"/swagger.json", "/openapi.json", "/v2/api-docs", "/api-docs"}

	for _, expected := range expectedPaths {
		found := false
		for _, path := range commonSwaggerPaths {
			if path == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected path %s not found in commonSwaggerPaths", expected)
		}
	}
}

func TestSwaggerSpec_JSON(t *testing.T) {
	spec := &SwaggerSpec{
		URL:           "http://example.com/swagger.json",
		Version:       "2.0",
		Title:         "Test API",
		Description:   "A test API",
		BasePath:      "/v1",
		Host:          "api.example.com",
		EndpointCount: 10,
		Tags:          []string{"users", "products"},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("Failed to marshal spec: %v", err)
	}

	var decoded SwaggerSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal spec: %v", err)
	}

	if decoded.Version != spec.Version {
		t.Errorf("Version mismatch: %s != %s", decoded.Version, spec.Version)
	}
	if decoded.Title != spec.Title {
		t.Errorf("Title mismatch: %s != %s", decoded.Title, spec.Title)
	}
}

func TestSwaggerEndpoint_JSON(t *testing.T) {
	endpoint := &SwaggerEndpoint{
		URL:         "http://example.com/users",
		Method:      "POST",
		Path:        "/users",
		Summary:     "Create user",
		Tags:        []string{"users"},
		ContentType: "application/json",
		Deprecated:  false,
	}

	data, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatalf("Failed to marshal endpoint: %v", err)
	}

	var decoded SwaggerEndpoint
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal endpoint: %v", err)
	}

	if decoded.URL != endpoint.URL {
		t.Errorf("URL mismatch: %s != %s", decoded.URL, endpoint.URL)
	}
	if decoded.Method != endpoint.Method {
		t.Errorf("Method mismatch: %s != %s", decoded.Method, endpoint.Method)
	}
}

func BenchmarkSwaggerDiscoverer_ParseSpec(b *testing.B) {
	swagger2Spec := map[string]interface{}{
		"swagger": "2.0",
		"info":    map[string]interface{}{"title": "Test", "version": "1.0"},
		"paths": map[string]interface{}{
			"/users": map[string]interface{}{
				"get":    map[string]interface{}{"summary": "Get users"},
				"post":   map[string]interface{}{"summary": "Create user"},
				"put":    map[string]interface{}{"summary": "Update user"},
				"delete": map[string]interface{}{"summary": "Delete user"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(swagger2Spec)
	}))
	defer server.Close()

	config := DefaultSwaggerConfig()
	config.Timeout = 5 * time.Second

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		discoverer := NewSwaggerDiscoverer(nil, config)
		discoverer.fetchAndParseSpec(server.URL)
	}
}
