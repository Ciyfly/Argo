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
