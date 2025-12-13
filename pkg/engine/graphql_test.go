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

func TestGraphQLConfig_Default(t *testing.T) {
	config := DefaultGraphQLConfig()

	if !config.Enabled {
		t.Error("Expected Enabled to be true by default")
	}
	if !config.AutoDiscover {
		t.Error("Expected AutoDiscover to be true by default")
	}
	if !config.EnableIntrospection {
		t.Error("Expected EnableIntrospection to be true by default")
	}
	if config.MaxDepth != 3 {
		t.Errorf("Expected MaxDepth=3, got %d", config.MaxDepth)
	}
	if config.Timeout != 10*time.Second {
		t.Errorf("Expected Timeout=10s, got %v", config.Timeout)
	}
	if !config.ExtractQueries {
		t.Error("Expected ExtractQueries to be true by default")
	}
	if !config.GenerateSampleQueries {
		t.Error("Expected GenerateSampleQueries to be true by default")
	}
}

func TestGraphQLDiscoverer_Basic(t *testing.T) {
	config := &GraphQLConfig{
		Enabled:             true,
		AutoDiscover:        true,
		EnableIntrospection: true,
		MaxDepth:            3,
		Timeout:             5 * time.Second,
	}

	discoverer := NewGraphQLDiscoverer(nil, config)
	if discoverer == nil {
		t.Fatal("NewGraphQLDiscoverer returned nil")
	}

	// 验证初始状态
	endpoints := discoverer.GetEndpoints()
	if len(endpoints) != 0 {
		t.Errorf("Expected 0 endpoints initially, got %d", len(endpoints))
	}

	schemas := discoverer.GetSchemas()
	if len(schemas) != 0 {
		t.Errorf("Expected 0 schemas initially, got %d", len(schemas))
	}

	queries := discoverer.GetQueries()
	if len(queries) != 0 {
		t.Errorf("Expected 0 queries initially, got %d", len(queries))
	}
}

func TestGraphQLDiscoverer_AddEndpoint(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	// 通过内部方法添加端点
	discoverer.addEndpoint(&GraphQLEndpoint{
		URL:          "https://example.com/graphql",
		Method:       "POST",
		DiscoveredAt: time.Now(),
		Source:       "test",
		Verified:     true,
	})

	endpoints := discoverer.GetEndpoints()
	if len(endpoints) != 1 {
		t.Errorf("Expected 1 endpoint, got %d", len(endpoints))
	}

	// 重复添加应该被忽略
	discoverer.addEndpoint(&GraphQLEndpoint{
		URL:    "https://example.com/graphql",
		Method: "POST",
		Source: "test2",
	})

	endpoints = discoverer.GetEndpoints()
	if len(endpoints) != 1 {
		t.Errorf("Expected 1 endpoint after duplicate add, got %d", len(endpoints))
	}
}

func TestGraphQLDiscoverer_AnalyzeContent(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	// 测试 gql 模板字符串检测
	jsContent := `
		const query = gql` + "`" + `
			query GetUsers {
				users {
					id
					name
				}
			}
		` + "`" + `;
	`

	discoverer.AnalyzeContent(jsContent, "https://example.com/app.js")

	queries := discoverer.GetQueries()
	if len(queries) == 0 {
		t.Error("Expected to extract queries from gql template")
	}
}

func TestGraphQLDiscoverer_ExtractEndpointURLs(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	content := `
		const API_URL = "https://api.example.com/graphql";
		fetch("/api/gql", { method: "POST" });
	`

	discoverer.AnalyzeContent(content, "https://example.com/app.js")

	endpoints := discoverer.GetEndpoints()
	if len(endpoints) < 1 {
		t.Errorf("Expected at least 1 endpoint from content, got %d", len(endpoints))
	}
}

func TestGraphQLDiscoverer_QueryTypeDetection(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	tests := []struct {
		query    string
		expected string
	}{
		{"query GetUsers { users { id } }", "query"},
		{"mutation CreateUser { createUser { id } }", "mutation"},
		{"subscription OnUserCreated { userCreated { id } }", "subscription"},
		{"{ users { id } }", "query"}, // 匿名查询默认为 query
	}

	for _, tt := range tests {
		result := discoverer.detectQueryType(tt.query)
		if result != tt.expected {
			t.Errorf("detectQueryType(%q) = %q, want %q", tt.query, result, tt.expected)
		}
	}
}

func TestGraphQLDiscoverer_ValidateQuery(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	tests := []struct {
		query    string
		expected bool
	}{
		{"query GetUsers { users { id } }", true},
		{"mutation CreateUser { createUser { id } }", true},
		{"{ users { id } }", true},
		{"short", false},  // 太短
		{"no braces", false}, // 没有大括号且没有关键字
	}

	for _, tt := range tests {
		result := discoverer.isValidGraphQLQuery(tt.query)
		if result != tt.expected {
			t.Errorf("isValidGraphQLQuery(%q) = %v, want %v", tt.query, result, tt.expected)
		}
	}
}

func TestGraphQLDiscoverer_GetTypeName(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	tests := []struct {
		typeRef  *GraphQLTypeRef
		expected string
	}{
		{nil, "String"},
		{&GraphQLTypeRef{Kind: "SCALAR", Name: "String"}, "String"},
		{&GraphQLTypeRef{Kind: "SCALAR", Name: "Int"}, "Int"},
		{&GraphQLTypeRef{Kind: "NON_NULL", OfType: &GraphQLTypeRef{Kind: "SCALAR", Name: "String"}}, "String!"},
		{&GraphQLTypeRef{Kind: "LIST", OfType: &GraphQLTypeRef{Kind: "SCALAR", Name: "String"}}, "[String]"},
	}

	for _, tt := range tests {
		result := discoverer.getTypeName(tt.typeRef)
		if result != tt.expected {
			t.Errorf("getTypeName() = %q, want %q", result, tt.expected)
		}
	}
}

func TestGraphQLDiscoverer_Stats(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	// 添加一些数据
	discoverer.addEndpoint(&GraphQLEndpoint{
		URL:      "https://example.com/graphql",
		Method:   "POST",
		Verified: true,
		Source:   "test",
	})
	discoverer.addEndpoint(&GraphQLEndpoint{
		URL:      "https://example.com/gql",
		Method:   "POST",
		Verified: false,
		Source:   "test",
	})
	discoverer.addExtractedQuery(&ExtractedQuery{
		Name:   "GetUsers",
		Type:   "query",
		Query:  "query GetUsers { users { id } }",
		Source: "test",
	})
	discoverer.addExtractedQuery(&ExtractedQuery{
		Name:   "CreateUser",
		Type:   "mutation",
		Query:  "mutation CreateUser { createUser { id } }",
		Source: "test",
	})

	stats := discoverer.GetStats()

	if stats.EndpointsDiscovered != 2 {
		t.Errorf("Expected EndpointsDiscovered=2, got %d", stats.EndpointsDiscovered)
	}
	if stats.EndpointsVerified != 1 {
		t.Errorf("Expected EndpointsVerified=1, got %d", stats.EndpointsVerified)
	}
	if stats.QueriesExtracted != 2 {
		t.Errorf("Expected QueriesExtracted=2, got %d", stats.QueriesExtracted)
	}
	if stats.MutationsFound != 1 {
		t.Errorf("Expected MutationsFound=1, got %d", stats.MutationsFound)
	}
}

func TestGraphQLDiscoverer_Clear(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	// 添加数据
	discoverer.addEndpoint(&GraphQLEndpoint{
		URL:    "https://example.com/graphql",
		Method: "POST",
		Source: "test",
	})
	discoverer.addExtractedQuery(&ExtractedQuery{
		Name:   "GetUsers",
		Query:  "query GetUsers { users { id } }",
		Source: "test",
	})

	// 清空
	discoverer.Clear()

	endpoints := discoverer.GetEndpoints()
	if len(endpoints) != 0 {
		t.Errorf("Expected 0 endpoints after Clear, got %d", len(endpoints))
	}

	queries := discoverer.GetQueries()
	if len(queries) != 0 {
		t.Errorf("Expected 0 queries after Clear, got %d", len(queries))
	}
}

func TestGraphQLDiscoverer_GetResult(t *testing.T) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	// 添加数据
	discoverer.addEndpoint(&GraphQLEndpoint{
		URL:      "https://example.com/graphql",
		Method:   "POST",
		Source:   "test",
		Verified: true,
	})

	result := discoverer.GetResult()

	if result == nil {
		t.Fatal("GetResult returned nil")
	}
	if len(result.Endpoints) != 1 {
		t.Errorf("Expected 1 endpoint in result, got %d", len(result.Endpoints))
	}
	if result.Stats.EndpointsDiscovered != 1 {
		t.Errorf("Expected EndpointsDiscovered=1 in stats, got %d", result.Stats.EndpointsDiscovered)
	}
}

func TestGraphQLDiscoverer_Disabled(t *testing.T) {
	config := &GraphQLConfig{
		Enabled:      false,
		AutoDiscover: false,
	}

	discoverer := NewGraphQLDiscoverer(nil, config)

	// 禁用时 DiscoverFromBaseURL 应该返回 nil
	endpoints := discoverer.DiscoverFromBaseURL("https://example.com")
	if endpoints != nil {
		t.Error("Expected nil endpoints when disabled")
	}

	// 禁用时 AnalyzeContent 应该不执行任何操作
	discoverer.AnalyzeContent("gql`query { test }`", "https://example.com/app.js")
	queries := discoverer.GetQueries()
	if len(queries) != 0 {
		t.Error("Expected no queries when disabled")
	}
}

func TestGraphQLDiscoverer_ProbeEndpoint(t *testing.T) {
	// 创建模拟 GraphQL 服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// 返回简单的内省响应
		response := map[string]interface{}{
			"data": map[string]interface{}{
				"__schema": map[string]interface{}{
					"queryType": map[string]interface{}{
						"name": "Query",
					},
					"mutationType": nil,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := &GraphQLConfig{
		Enabled:             true,
		AutoDiscover:        true,
		EnableIntrospection: true,
		Timeout:             5 * time.Second,
	}

	discoverer := NewGraphQLDiscoverer(nil, config)

	// 探测端点
	endpoint := discoverer.probeEndpoint(server.URL)

	if endpoint == nil {
		t.Fatal("probeEndpoint returned nil for valid GraphQL server")
	}
	if endpoint.URL != server.URL {
		t.Errorf("Expected URL=%s, got %s", server.URL, endpoint.URL)
	}
	if !endpoint.Verified {
		t.Error("Expected endpoint to be verified")
	}
	if !endpoint.SupportsIntrospection {
		t.Error("Expected endpoint to support introspection")
	}
}

func TestGraphQLDiscoverer_ProbeEndpoint_NonGraphQL(t *testing.T) {
	// 创建非 GraphQL 服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>Not a GraphQL server</html>"))
	}))
	defer server.Close()

	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	// 探测端点应该返回 nil
	endpoint := discoverer.probeEndpoint(server.URL)

	if endpoint != nil {
		t.Error("probeEndpoint should return nil for non-GraphQL server")
	}
}

func TestGraphQLDiscoverer_Introspect(t *testing.T) {
	// 创建模拟 GraphQL 服务器（支持完整内省）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": map[string]interface{}{
				"__schema": map[string]interface{}{
					"queryType": map[string]interface{}{"name": "Query"},
					"mutationType": map[string]interface{}{"name": "Mutation"},
					"types": []interface{}{
						map[string]interface{}{
							"kind":        "OBJECT",
							"name":        "Query",
							"description": "Root query type",
							"fields": []interface{}{
								map[string]interface{}{
									"name": "users",
									"type": map[string]interface{}{
										"kind": "LIST",
										"ofType": map[string]interface{}{
											"kind": "OBJECT",
											"name": "User",
										},
									},
									"args": []interface{}{},
								},
							},
						},
						map[string]interface{}{
							"kind": "OBJECT",
							"name": "User",
							"fields": []interface{}{
								map[string]interface{}{
									"name": "id",
									"type": map[string]interface{}{
										"kind": "NON_NULL",
										"ofType": map[string]interface{}{
											"kind": "SCALAR",
											"name": "ID",
										},
									},
								},
								map[string]interface{}{
									"name": "name",
									"type": map[string]interface{}{
										"kind": "SCALAR",
										"name": "String",
									},
								},
							},
						},
					},
					"directives": []interface{}{},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := &GraphQLConfig{
		Enabled:               true,
		EnableIntrospection:   true,
		GenerateSampleQueries: true,
		Timeout:               5 * time.Second,
	}

	discoverer := NewGraphQLDiscoverer(nil, config)

	schema, err := discoverer.Introspect(server.URL)
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	if schema == nil {
		t.Fatal("Introspect returned nil schema")
	}

	if schema.QueryType == nil || schema.QueryType.Name != "Query" {
		t.Error("Expected QueryType.Name='Query'")
	}

	if schema.MutationType == nil || schema.MutationType.Name != "Mutation" {
		t.Error("Expected MutationType.Name='Mutation'")
	}

	// 检查是否过滤了 __ 开头的内置类型
	for _, typ := range schema.Types {
		if len(typ.Name) > 0 && typ.Name[0:1] == "_" {
			t.Errorf("Internal type %s should be filtered", typ.Name)
		}
	}
}

func TestGraphQLDiscoverer_Introspect_Disabled(t *testing.T) {
	config := &GraphQLConfig{
		Enabled:             true,
		EnableIntrospection: false, // 禁用内省
		Timeout:             5 * time.Second,
	}

	discoverer := NewGraphQLDiscoverer(nil, config)

	_, err := discoverer.Introspect("https://example.com/graphql")
	if err == nil {
		t.Error("Expected error when introspection is disabled")
	}
}

func TestGraphQLEndpoint_JSON(t *testing.T) {
	endpoint := &GraphQLEndpoint{
		URL:                   "https://example.com/graphql",
		Method:                "POST",
		DiscoveredAt:          time.Now(),
		SupportsIntrospection: true,
		Source:                "auto_discover",
		Verified:              true,
		ResponseTime:          100 * time.Millisecond,
	}

	data, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatalf("Failed to marshal endpoint: %v", err)
	}

	var decoded GraphQLEndpoint
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal endpoint: %v", err)
	}

	if decoded.URL != endpoint.URL {
		t.Errorf("URL mismatch: %s != %s", decoded.URL, endpoint.URL)
	}
	if decoded.SupportsIntrospection != endpoint.SupportsIntrospection {
		t.Error("SupportsIntrospection mismatch")
	}
}

func TestGraphQLResult_JSON(t *testing.T) {
	result := &GraphQLResult{
		Endpoints: []*GraphQLEndpoint{
			{URL: "https://example.com/graphql", Method: "POST"},
		},
		Schemas: []*GraphQLSchema{},
		Queries: []*ExtractedQuery{
			{Name: "GetUsers", Type: "query", Query: "query { users }"},
		},
		Stats: GraphQLStats{
			EndpointsDiscovered: 1,
			QueriesExtracted:    1,
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}

	var decoded GraphQLResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if len(decoded.Endpoints) != 1 {
		t.Errorf("Expected 1 endpoint, got %d", len(decoded.Endpoints))
	}
	if len(decoded.Queries) != 1 {
		t.Errorf("Expected 1 query, got %d", len(decoded.Queries))
	}
}

func TestCommonGraphQLPaths(t *testing.T) {
	// 确保常见路径列表包含预期的路径
	expectedPaths := []string{"/graphql", "/gql", "/api/graphql"}

	for _, expected := range expectedPaths {
		found := false
		for _, path := range commonGraphQLPaths {
			if path == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected path %s not found in commonGraphQLPaths", expected)
		}
	}
}

func BenchmarkGraphQLDiscoverer_AnalyzeContent(b *testing.B) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	content := `
		const query = gql` + "`" + `query GetUsers { users { id name email } }` + "`" + `;
		const mutation = gql` + "`" + `mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }` + "`" + `;
		const GRAPHQL_URL = "https://api.example.com/graphql";
		fetch("/api/gql", { method: "POST", body: JSON.stringify({ query }) });
	`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		discoverer.AnalyzeContent(content, "https://example.com/app.js")
	}
}

func BenchmarkGraphQLDiscoverer_ValidateQuery(b *testing.B) {
	config := DefaultGraphQLConfig()
	discoverer := NewGraphQLDiscoverer(nil, config)

	query := "query GetUsers($id: ID!, $limit: Int) { users(id: $id, limit: $limit) { id name email createdAt } }"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		discoverer.isValidGraphQLQuery(query)
	}
}
