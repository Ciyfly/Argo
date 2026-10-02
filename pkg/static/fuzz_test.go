package static

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"argo/pkg/conf"
	"argo/pkg/log"
)

func fuzzTestServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hitCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin":
			atomic.AddInt32(&hitCount, 1)
			w.WriteHeader(http.StatusOK)
		case "/old":
			// 同 host 301 重定向 → 有效（回指本服务器）
			atomic.AddInt32(&hitCount, 1)
			w.Header().Set("Location", "http://"+r.Host+"/new")
			w.WriteHeader(http.StatusMovedPermanently)
		case "/ext":
			// 异 host 301 → 无效
			w.Header().Set("Location", "https://other.example.com/x")
			w.WriteHeader(http.StatusMovedPermanently)
		case "/api":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return server, &hitCount
}

func TestFuzzPathsValidAndInvalid(t *testing.T) {
	conf.GlobalConfig = &conf.Conf{}
	log.Init(false, false)
	server, _ := fuzzTestServer(t)
	defer server.Close()

	// 用外部小字典控制探测集合，避免内置 120 条全量打
	dict := filepath.Join(t.TempDir(), "dict.txt")
	os.WriteFile(dict, []byte("admin\nold\next\nmissing\n#注释\n\n/api\n"), 0644)

	var found []string
	FuzzPaths(context.Background(), server.URL, dict, func(u string) { found = append(found, u) })

	// admin(200)、old(同host 301)、api(200) 有效；ext(异域 301)、missing(404)、注释/空行 无效
	if len(found) != 3 {
		t.Fatalf("应命中 admin/old/api 三条，实际: %v", found)
	}
	seen := map[string]bool{}
	for _, u := range found {
		seen[u] = true
	}
	for _, want := range []string{"/admin", "/old", "/api"} {
		if !seen[server.URL+want] {
			t.Errorf("缺少命中 %s，实际: %v", want, found)
		}
	}
}

func TestFuzzPathsDictFallback(t *testing.T) {
	conf.GlobalConfig = &conf.Conf{}
	// 文件不存在 → 回退内置字典
	paths := LoadFuzzPaths("/nonexistent/dict.txt")
	if len(paths) == 0 || len(paths) != len(builtInFuzzPaths) {
		t.Errorf("字典文件不存在应回退内置字典")
	}
	// 外部字典清洗：去注释/空行/前导斜杠
	tmp := t.TempDir()
	dict := filepath.Join(tmp, "d.txt")
	os.WriteFile(dict, []byte("# c\n\n/a\nb/\n"), 0644)
	paths = LoadFuzzPaths(dict)
	if len(paths) != 2 || paths[0] != "a" || paths[1] != "b/" {
		t.Errorf("字典清洗结果不对: %v", paths)
	}
}
