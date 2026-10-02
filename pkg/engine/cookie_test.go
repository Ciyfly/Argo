package engine

import "testing"

// 回归测试：Cookie 解析必须支持两种格式，且域名/路径绑定正确。
//
// 无 Cookie 注入时，登录后才能访问的入口（会员区/管理后台）整个漏掉——
// 靶场登录轨 argo 只有 25%，katana 靠 Cookie 注入拿 75%。
func TestParseCookies(t *testing.T) {
	target := "https://example.com/path"
	raw := []string{
		"session=abc123",                     // 默认绑目标域名
		"token=xyz@auth.example.com",         // 显式域名
		"bad-format",                         // 非法：无 =，应跳过
		"",                                   // 空串，应跳过
		"  spaced = spaced value  ",          // 前后空白
	}
	cs := parseCookies(raw, target)
	if len(cs) != 3 {
		t.Fatalf("应解析出 3 个合法 Cookie，实际 %d 个", len(cs))
	}

	// 第一个：默认绑定目标站点域名
	c0 := cs[0]
	if c0.Name != "session" || c0.Value != "abc123" {
		t.Errorf("第 1 个 cookie 解析错误: name=%q value=%q", c0.Name, c0.Value)
	}
	if c0.Domain != "example.com" {
		t.Errorf("第 1 个 cookie 域名应为目标站点 example.com，实际 %q", c0.Domain)
	}

	// 第二个：显式域名优先
	c1 := cs[1]
	if c1.Name != "token" || c1.Value != "xyz" {
		t.Errorf("第 2 个 cookie 解析错误: name=%q value=%q", c1.Name, c1.Value)
	}
	if c1.Domain != "auth.example.com" {
		t.Errorf("第 2 个 cookie 域名应为显式指定的 auth.example.com，实际 %q", c1.Domain)
	}

	// 路径必须是 /，否则登录后的子路径（/member/xxx）带不上
	for i, c := range cs {
		if c.Path != "/" {
			t.Errorf("cookie[%d] Path 应为 /，实际 %q", i, c.Path)
		}
	}
}

// 回归测试：value 里含 = 号不能被截断（session token 常见 base64 含 =）。
func TestParseCookiesValueWithEquals(t *testing.T) {
	cs := parseCookies([]string{"tok=abc=def=="}, "https://example.com")
	if len(cs) != 1 {
		t.Fatalf("应解析出 1 个 cookie，实际 %d", len(cs))
	}
	if cs[0].Value != "abc=def==" {
		t.Errorf("value 含 = 时应保留完整值，实际 %q", cs[0].Value)
	}
}

// 回归测试：空列表返回 nil，不产生无效注入。
func TestParseCookiesEmpty(t *testing.T) {
	if cs := parseCookies(nil, "https://example.com"); cs != nil {
		t.Errorf("空输入应返回 nil")
	}
	if cs := parseCookies([]string{"", "bad"}, "https://example.com"); len(cs) != 0 {
		t.Errorf("全非法输入应返回空，实际 %d 个", len(cs))
	}
}
