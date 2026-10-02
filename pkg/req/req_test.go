package req

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 回归测试：GetResponseWithBody 在状态码 200 时必须返回响应，否则 robots.txt /
// sitemap.xml 解析拿不到内容（历史上这里逻辑写反，导致该功能完全失效）。
func TestGetResponseWithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "User-agent: *\nDisallow: /admin/\n")
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	conf.GlobalConfig = &conf.Conf{}
	// 日志在 main 里初始化，测试里需要自己初始化，否则 Logger 为 nil 会 panic
	log.Init(false, false)

	// 200 必须返回非 nil，且能读到 body
	resp := GetResponseWithBody(server.URL + "/robots.txt")
	if resp == nil {
		t.Fatalf("GetResponseWithBody 200 返回了 nil，robots.txt/sitemap.xml 解析会直接失效")
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body err: %s", err)
	}
	if len(body) == 0 {
		t.Errorf("200 响应的 body 为空")
	}

	// 非 200 必须返回 nil
	if resp := GetResponseWithBody(server.URL + "/missing"); resp != nil {
		resp.Body.Close()
		t.Errorf("非 200 响应应当返回 nil，实际拿到 status=%d", resp.StatusCode)
	}
}

// 回归测试：DoWithRetry 在服务器暂时失败后重试应当最终成功，
// retries=0 时保持原有单次行为。
func TestDoWithRetry(t *testing.T) {
	failCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 前两次连接级别的失败用直接关闭连接模拟
		if r.URL.Path == "/flaky" && failCount < 2 {
			failCount++
			panic(http.ErrAbortHandler)
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	defer server.Close()

	conf.GlobalConfig = &conf.Conf{}
	log.Init(false, false)

	request, _ := http.NewRequest("GET", server.URL+"/flaky", nil)

	// retries=2：两次失败后第三次成功
	resp, err := DoWithRetry(http.DefaultClient, request, 2)
	if err != nil {
		t.Fatalf("retries=2 时应当重试成功，实际 err: %s", err)
	}
	resp.Body.Close()

	// 失败计数重置后再验证 retries=0 不重试
	failCount = 0
	request2, _ := http.NewRequest("GET", server.URL+"/flaky", nil)
	_, err = DoWithRetry(http.DefaultClient, request2, 0)
	if err == nil {
		t.Errorf("retries=0 时首次失败应当直接返回错误，不应重试")
	}
}

// 回归测试：内容类型探测按响应区分 html/json，失败时 Ok=false 供调用方回退。
func TestProbeContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
		case "/api/data":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
		case "/noct":
			// 无 Content-Type
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	conf.GlobalConfig = &conf.Conf{}
	log.Init(false, false)

	if got := ProbeContentType(server.URL + "/page"); !got.Ok || !IsHTMLContentType(got.ContentType) {
		t.Errorf("html 响应应判定为 HTML: %+v", got)
	}
	if got := ProbeContentType(server.URL + "/api/data"); !got.Ok || IsHTMLContentType(got.ContentType) {
		t.Errorf("json 响应不应判定为 HTML: %+v", got)
	}
	if got := ProbeContentType(server.URL + "/noct"); !got.Ok || got.ContentType != "" {
		t.Errorf("无 Content-Type 响应应返回空类型: %+v", got)
	}
	if got := ProbeContentType("http://127.0.0.1:1/unreachable"); got.Ok {
		t.Errorf("不可达目标应返回 Ok=false: %+v", got)
	}
}
