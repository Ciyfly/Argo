package extract

import (
	"strings"
	"testing"
)

func TestExtractAPIPathsFromJS(t *testing.T) {
	js := `
		var config = { logoutUrl: "/user/logout.php" };
		fetch("/api/v1/user/list?page=1");
		axios.post('/api/order/create');
		$.get("user/detail?id=3");
		var cdn = "https://cdn.example.com/lib.js";
		var noise = "not a path";
	`
	got := ExtractAPIPaths(js, "https://a.com/static/app.js")

	want := []string{
		"https://a.com/user/logout.php",
		"https://a.com/api/v1/user/list?page=1",
		"https://a.com/api/order/create",
		// 相对路径基于 JS 文件自身 URL 解析（与浏览器相对路径语义一致）
		"https://a.com/static/user/detail?id=3",
		"https://cdn.example.com/lib.js",
	}
	for _, wantURL := range want {
		found := false
		for _, gotURL := range got {
			if gotURL == wantURL {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("应当提取到 %s，实际提取结果: %v", wantURL, got)
		}
	}
}

func TestExtractAPIPathsSkips(t *testing.T) {
	js := `
		fetch("data:application/json,{}");
		fetch("{{apiBase}}/user");
		fetch("webpack://chunk/1");
		open("javascript:void(0)");
	`
	got := ExtractAPIPaths(js, "https://a.com/app.js")
	if len(got) != 0 {
		t.Errorf("噪声候选不应入队，实际: %v", got)
	}
}

func TestExtractAPIPathsEmpty(t *testing.T) {
	if got := ExtractAPIPaths("", "https://a.com/a.js"); len(got) != 0 {
		t.Errorf("空内容不应有提取结果: %v", got)
	}
	// 非法 base URL 下相对路径无法解析，直接丢弃而不是 panic
	if got := ExtractAPIPaths(`fetch("/api/x")`, ":::"); len(got) != 0 {
		t.Errorf("base 非法时相对路径应被丢弃: %v", got)
	}
}

func TestExtractFromHTMLInlineScript(t *testing.T) {
	// 内联 script 里的路径数组（DOM 交互不揭示、DOM 解析看不到的场景）
	html := `<script>var chain = ["/plain/link", "/classic/menu-l3-detail.html", "/api/v1/whoami"];</script>`
	got := ExtractAPIPaths(html, "https://a.com/index.html")
	want := []string{
		"https://a.com/plain/link",
		"https://a.com/classic/menu-l3-detail.html",
		"https://a.com/api/v1/whoami",
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("应提取到 %s，实际: %v", w, got)
		}
	}
}

func TestIsDocumentURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://a.com/page.html", true},
		{"https://a.com/user/logout.php?x=1", true},
		{"https://a.com/a.jsp#frag", true},
		{"https://a.com/api/v1/user/list", false},
		{"https://a.com/api/order?id=3", false},
		{"https://a.com/", false},
	}
	for _, c := range cases {
		if got := IsDocumentURL(c.url); got != c.want {
			t.Errorf("IsDocumentURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestIsJSResponse(t *testing.T) {
	cases := []struct {
		url, contentType string
		want             bool
	}{
		{"https://a.com/app.js", "", true},
		{"https://a.com/app.JS", "", true},
		{"https://a.com/app.mjs", "", true},
		{"https://a.com/api/data", "application/javascript", true},
		{"https://a.com/page", "text/html", false},
		{"https://a.com/data.json", "application/json", false},
	}
	for _, c := range cases {
		if got := IsJSResponse(c.url, c.contentType); got != c.want {
			t.Errorf("IsJSResponse(%q, %q) = %v, want %v", c.url, c.contentType, got, c.want)
		}
	}
}

func TestResponseProcessDispatch(t *testing.T) {
	if err := InitSecretRules(nil); err != nil {
		t.Fatalf("InitSecretRules err: %s", err)
	}
	var endpoints, secrets []string
	input := &ResponseInput{
		URL:         "https://a.com/app.js",
		ContentType: "application/javascript",
		Body:        []byte(`fetch("/api/user"); var token = "AKIAIOSFODNN7EXAMPLE";`),
		OnEndpoint:  func(u string) { endpoints = append(endpoints, u) },
		OnSecret:    func(f SecretFinding) { secrets = append(secrets, f.Rule) },
	}
	input.Process()
	if len(endpoints) != 1 || !strings.HasSuffix(endpoints[0], "/api/user") {
		t.Errorf("JS 响应应提取接口，实际: %v", endpoints)
	}
	if len(secrets) != 1 || secrets[0] != "AWS AccessKey" {
		t.Errorf("JS 响应应检出 AWS AccessKey，实际: %v", secrets)
	}
}

func TestResponseProcessBodyTooLarge(t *testing.T) {
	called := false
	input := &ResponseInput{
		URL:         "https://a.com/app.js",
		ContentType: "application/javascript",
		Body:        make([]byte, MaxScanBodySize+1),
		OnEndpoint:  func(u string) { called = true },
	}
	input.Process()
	if called {
		t.Errorf("超过大小上限的响应不应提取")
	}
}
