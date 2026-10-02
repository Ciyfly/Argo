package scope

import (
	"argo/pkg/conf"
	"testing"
)

func initTestScope(t *testing.T, target string, cfg conf.ScopeConf) {
	t.Helper()
	InitFromTarget(target, cfg)
}

func TestIsInScopeExactHost(t *testing.T) {
	initTestScope(t, "http://a.com/", conf.ScopeConf{})

	cases := []struct {
		url  string
		want bool
		desc string
	}{
		{"http://a.com/", true, "目标本身"},
		{"https://a.com/page?id=1", true, "同 host 任意 scheme"},
		{"http://a.com:8080/admin", true, "同 host 任意端口（默认宽松）"},
		{"http://evil-a.com/", false, "前缀拼接域名不算域内（修复子串误匹配）"},
		{"http://x.a.com.evil.io/", false, "目标作为中缀的域名不算域内"},
		{"http://sub.a.com/", false, "默认不爬子域"},
		{"", false, "空 URL"},
		{"::::not-a-url", false, "非法 URL"},
	}
	for _, c := range cases {
		if got := IsInScope(c.url); got != c.want {
			t.Errorf("%s: IsInScope(%q) = %v, want %v", c.desc, c.url, got, c.want)
		}
	}
}

func TestIsInScopeSubdomain(t *testing.T) {
	initTestScope(t, "https://a.com/", conf.ScopeConf{CrawlSubdomains: true})

	if !IsInScope("http://dev.a.com/x") {
		t.Errorf("crawl_subdomains 开启后子域应当在范围内")
	}
	if !IsInScope("http://a.com/") {
		t.Errorf("目标本身仍然应当在范围内")
	}
	if IsInScope("http://a.com.evil.io/") {
		t.Errorf("以目标为前缀的恶意域名不应当因后缀规则误入")
	}
	if IsInScope("http://nota.com/") {
		t.Errorf("后缀相同的其它域名不应当在范围内")
	}
}

func TestIsInScopeIncludeExclude(t *testing.T) {
	cfg := conf.ScopeConf{
		Include: []string{`^https?://[a-z]+\.partner\.com/`},
		Exclude: []string{`/logout`},
	}
	initTestScope(t, "http://a.com/", cfg)

	if !IsInScope("https://api.partner.com/v1/user") {
		t.Errorf("include 正则命中的 URL 应当入域")
	}
	if IsInScope("http://a.com/user/logout") {
		t.Errorf("exclude 正则命中的 URL 应当出域（优先级最高）")
	}
	if !IsInScope("http://a.com/user/login") {
		t.Errorf("未命中 exclude 的域内 URL 应当保持入域")
	}
}

func TestOutScopeRecord(t *testing.T) {
	ResetOutScope()
	RecordOutScope("http://evil.com/1")
	RecordOutScope("http://evil.com/1") // 重复不计数
	RecordOutScope("")
	got := SnapshotOutScope()
	if len(got) != 1 || got[0] != "http://evil.com/1" {
		t.Errorf("域外记录应当去重且忽略空值，实际: %v", got)
	}
	ResetOutScope()
	if got := SnapshotOutScope(); len(got) != 0 {
		t.Errorf("ResetOutScope 后应当为空，实际: %v", got)
	}
}
