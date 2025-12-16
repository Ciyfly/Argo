package static

import (
	"strings"
	"testing"
)

func TestJSluiceAnalyzer_BasicURLExtraction(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	jsCode := `
		function loadData() {
			fetch('/api/users', { method: 'POST', headers: { 'Content-Type': 'application/json' } });
			fetch('/api/v1/products');
			$.ajax({ url: '/api/orders', method: 'GET' });
			$.get('/api/categories');
			$.post('/api/cart/add', { item: 1 });
		}
	`

	result := analyzer.AnalyzeJS(jsCode)

	if result.TotalURLs == 0 {
		t.Error("Expected to find URLs, got 0")
	}

	t.Logf("Found %d URLs, %d APIs", result.TotalURLs, result.TotalAPIs)

	for _, u := range result.URLs {
		t.Logf("URL: %s, Type: %s, Method: %s", u.URL, u.Type, u.Method)
	}
}

func TestJSluiceAnalyzer_LocationAssignment(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	jsCode := `
		function redirect() {
			document.location = '/login';
			window.location.href = '/dashboard';
			location.assign('/home');
		}
	`

	result := analyzer.AnalyzeJS(jsCode)

	if result.TotalURLs == 0 {
		t.Error("Expected to find URLs from location assignments")
	}

	t.Logf("Found %d URLs from location assignments", result.TotalURLs)
	for _, u := range result.URLs {
		t.Logf("URL: %s, Type: %s", u.URL, u.Type)
	}
}

func TestJSluiceAnalyzer_FiltersNonURLTokens(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	// 这些字符串在真实 SPA bundle 中经常出现（viewport/X-UA-Compatible/renderer 等），
	// 但并不是 URL。这里用 location 赋值触发 jsluice 的 URL 采集，再验证过滤生效。
	jsCode := `
		document.location = 'width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no';
		window.location.href = 'IE=edge,chrome=1';
		location.assign('webkit');
		fetch('/api/users');
	`

	result := analyzer.AnalyzeJS(jsCode)

	for _, u := range result.URLs {
		if u.URL == "webkit" || u.URL == "IE=edge,chrome=1" || strings.Contains(u.URL, "width=device-width") {
			t.Fatalf("expected non-url token to be filtered, got: %q (type=%s)", u.URL, u.Type)
		}
	}

	found := false
	for _, u := range result.URLs {
		if u.URL == "/api/users" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected api url to remain, got=%v", result.URLs)
	}
}

func TestJSluiceAnalyzer_XMLHttpRequest(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	jsCode := `
		function makeRequest() {
			var xhr = new XMLHttpRequest();
			xhr.open('POST', '/api/submit');
			xhr.setRequestHeader('Authorization', 'Bearer token123');
			xhr.send(JSON.stringify({data: 'test'}));
		}
	`

	result := analyzer.AnalyzeJS(jsCode)

	if result.TotalURLs == 0 {
		t.Error("Expected to find URLs from XMLHttpRequest")
	}

	t.Logf("Found %d URLs from XHR", result.TotalURLs)
	for _, u := range result.URLs {
		t.Logf("URL: %s, Method: %s", u.URL, u.Method)
	}
}

func TestJSluiceAnalyzer_RouterDefinitions(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	// Vue Router 风格
	jsCode := `
		const routes = [
			{ path: '/users', component: UserList },
			{ path: '/users/:id', component: UserDetail },
			{ path: '/admin/dashboard', component: AdminDashboard },
			{ path: '/api/settings', name: 'settings' }
		];
	`

	result := analyzer.AnalyzeJS(jsCode)

	t.Logf("Found %d URLs from router definitions", result.TotalURLs)
	for _, u := range result.URLs {
		t.Logf("URL: %s, Type: %s", u.URL, u.Type)
	}
}

func TestJSluiceAnalyzer_StringConcatenation(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	// 字符串拼接
	jsCode := `
		const API_BASE = '/api/v2';
		const endpoint = API_BASE + '/users';
		fetch(API_BASE + '/products');
	`

	result := analyzer.AnalyzeJS(jsCode)

	t.Logf("Found %d URLs (string concatenation may have limited support)", result.TotalURLs)
	for _, u := range result.URLs {
		t.Logf("URL: %s", u.URL)
	}
}

func TestJSluiceAnalyzer_SecretDetection(t *testing.T) {
	config := DefaultJSluiceConfig()
	config.EnableSecretDetection = true
	analyzer := NewJSluiceAnalyzer(config)

	jsCode := `
		const config = {
			awsKey: 'AKIAIOSFODNN7EXAMPLE',
			awsSecret: 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY',
			apiKey: 'sk-1234567890abcdef1234567890abcdef',
			jwtToken: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U'
		};
	`

	result := analyzer.AnalyzeJS(jsCode)

	if len(result.Secrets) == 0 {
		t.Log("No secrets detected (this may be expected depending on jsluice patterns)")
	} else {
		t.Logf("Found %d types of secrets", len(result.Secrets))
		for _, s := range result.Secrets {
			t.Logf("Secret kind: %s, severity: %s", s.Kind, s.Severity)
		}
	}
}

func TestJSluiceAnalyzer_APIEndpointClassification(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	jsCode := `
		// API calls
		fetch('/api/users');
		$.ajax({ url: '/rest/v1/products' });

		// Non-API URLs
		document.location = '/home';
		window.open('/about');
	`

	result := analyzer.AnalyzeJS(jsCode)

	t.Logf("Total URLs: %d, API Endpoints: %d", result.TotalURLs, result.TotalAPIs)

	for _, api := range result.APIEndpoints {
		t.Logf("API: %s, Method: %s, Params: %v", api.URL, api.Method, api.QueryParams)
	}
}

func TestJSluiceAnalyzer_WithBaseURL(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	jsCode := `
		fetch('/api/users');
		fetch('//cdn.example.com/script.js');
		fetch('https://external.com/api');
	`

	urls := analyzer.GetURLStringsWithBase(jsCode, "https://example.com")

	t.Logf("Found %d URLs with base URL resolution", len(urls))
	for _, u := range urls {
		t.Logf("Resolved URL: %s", u)
	}
}

func TestParseJSWithJSluice(t *testing.T) {
	jsCode := `
		function init() {
			fetch('/api/init');
			$.get('/api/config');
			document.location = '/dashboard';
		}
	`

	urls := ParseJSWithJSluice(jsCode, "https://example.com")

	if len(urls) == 0 {
		t.Error("Expected ParseJSWithJSluice to find URLs")
	}

	t.Logf("ParseJSWithJSluice found %d URLs", len(urls))
	for _, u := range urls {
		t.Logf("URL: %s", u)
	}
}

func TestParseJSForAPIs(t *testing.T) {
	jsCode := `
		fetch('/api/users', { method: 'POST', body: JSON.stringify({name: 'test'}) });
		$.ajax({ url: '/api/products', method: 'GET', data: { page: 1 } });
	`

	apis := ParseJSForAPIs(jsCode)

	t.Logf("Found %d API endpoints", len(apis))
	for _, api := range apis {
		t.Logf("API: %s, Method: %s, QueryParams: %v, BodyParams: %v",
			api.URL, api.Method, api.QueryParams, api.BodyParams)
	}
}

func TestJSluiceAnalyzer_AxiosSupport(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	jsCode := `
		axios.get('/api/users');
		axios.post('/api/users', { name: 'John' });
		axios.put('/api/users/1', { name: 'Jane' });
		axios.delete('/api/users/1');
		axios({
			method: 'patch',
			url: '/api/users/1',
			data: { status: 'active' }
		});
	`

	result := analyzer.AnalyzeJS(jsCode)

	t.Logf("Found %d URLs from axios calls", result.TotalURLs)
	for _, u := range result.URLs {
		t.Logf("URL: %s, Method: %s, Type: %s", u.URL, u.Method, u.Type)
	}
}

func TestJSluiceAnalyzer_ComplexJavaScript(t *testing.T) {
	analyzer := NewJSluiceAnalyzer(nil)

	// 复杂的真实世界 JavaScript
	jsCode := `
		(function() {
			'use strict';

			const API = {
				baseUrl: '/api/v2',
				endpoints: {
					users: '/users',
					products: '/products',
					orders: '/orders'
				}
			};

			class DataService {
				constructor() {
					this.client = axios.create({
						baseURL: API.baseUrl,
						headers: { 'X-API-Key': 'secret123' }
					});
				}

				async getUsers() {
					return await fetch(API.baseUrl + API.endpoints.users);
				}

				async createOrder(data) {
					return await $.post('/api/v2/orders', data);
				}
			}

			// Event handlers
			document.getElementById('login').onclick = function() {
				window.location.href = '/auth/login?redirect=/dashboard';
			};

			// Dynamic URL generation
			const userId = 123;
			fetch('/api/users/' + userId + '/profile');

			// GraphQL
			fetch('/graphql', {
				method: 'POST',
				body: JSON.stringify({ query: '{ users { id name } }' })
			});
		})();
	`

	result := analyzer.AnalyzeJS(jsCode)

	t.Logf("Complex JS analysis - URLs: %d, APIs: %d, Secrets: %d",
		result.TotalURLs, result.TotalAPIs, len(result.Secrets))

	for _, u := range result.URLs {
		t.Logf("URL: %s, Type: %s, Method: %s", u.URL, u.Type, u.Method)
	}
}

func BenchmarkJSluiceAnalyzer(b *testing.B) {
	analyzer := NewJSluiceAnalyzer(nil)

	jsCode := `
		fetch('/api/users');
		$.ajax({ url: '/api/products' });
		document.location = '/home';
	`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.AnalyzeJS(jsCode)
	}
}

func BenchmarkParseJSWithJSluice(b *testing.B) {
	jsCode := `
		fetch('/api/users');
		$.ajax({ url: '/api/products' });
		document.location = '/home';
	`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ParseJSWithJSluice(jsCode, "")
	}
}
