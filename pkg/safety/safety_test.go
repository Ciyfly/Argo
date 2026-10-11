package safety

import (
	"encoding/json"
	"strings"
	"testing"
)

// resetState 各用例间复位全局状态（单进程内共享，测试必须显式重置）。
func resetState(enabled, autoProfile bool, profile string) {
	profileStrict.Store(false)
	Init(enabled, autoProfile, profile, DefaultUrlSkip(), DefaultFormSubmitSkip())
}

// DefaultUrlSkip/DefaultFormSubmitSkip 转发 conf 的默认列表，测试避免 import conf 造成环。
func DefaultUrlSkip() []string {
	return []string{"logout|signout|sign-out"}
}

func DefaultFormSubmitSkip() []string {
	return []string{
		"logout|signout|sign-out|注销|登出",
		"password_new|password_conf|password_current|old_?pass(word)?|new_?pass(word)?|curr_?pass(word)?|修改密码|更改密码|重置密码",
		"delete_?(user|account)|remove_?(user|account)|删除用户|删除账号",
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func TestShouldSkipURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"logout绝对地址", "http://127.0.0.1:13001/logout.php", true},
		{"signout路径", "https://example.com/account/signout", true},
		{"sign-out连字符", "https://example.com/sign-out/", true},
		{"普通页面不拦", "http://127.0.0.1:13001/vulnerabilities/csrf/", false},
		{"子串匹配已知行为", "https://example.com/api/logout-list", true}, // logout 出现即拦（子串语义），记录已知行为
	}
	resetState(true, true, "default")
	for _, c := range cases {
		if got := ShouldSkipURL(c.url); got != c.want {
			t.Errorf("%s: ShouldSkipURL(%q) = %v, want %v", c.name, c.url, got, c.want)
		}
	}
}

func TestShouldSkipURLDisabled(t *testing.T) {
	resetState(false, true, "default")
	if ShouldSkipURL("http://example.com/logout.php") {
		t.Error("enabled=false 时不应拦截任何 URL")
	}
}

func TestObserveHTMLFingerprint(t *testing.T) {
	dvwaHTML := `<title>Login :: Damn Vulnerable Web Application (DVWA)</title><form action='login.php'>`
	normalHTML := `<title>企业运营管理平台</title><form action='/login'>`

	resetState(true, true, "default")
	if IsStrict() {
		t.Fatal("初始不应是 strict")
	}
	ObserveHTML(normalHTML)
	if IsStrict() {
		t.Error("普通站点 HTML 不应命中指纹")
	}
	ObserveHTML(dvwaHTML)
	if !IsStrict() {
		t.Error("DVWA HTML 应命中指纹进入 strict")
	}

	// autoProfile=false 时不识别
	resetState(true, false, "default")
	ObserveHTML(dvwaHTML)
	if IsStrict() {
		t.Error("auto_profile=false 时不应命中指纹")
	}

	// 手动 strict 不依赖指纹
	resetState(true, false, "strict")
	if !IsStrict() {
		t.Error("profile=strict 应直接生效")
	}

	// disabled 时指纹识别关闭
	resetState(false, true, "default")
	ObserveHTML(dvwaHTML)
	if IsStrict() {
		t.Error("enabled=false 时指纹识别应关闭")
	}
}

func TestFormSkipPayload(t *testing.T) {
	var payload struct {
		Skip []string `json:"skip"`
	}

	// default：含登出/改密码/删除，不含设置类
	resetState(true, true, "default")
	if err := json.Unmarshal([]byte(FormSkipPayload()), &payload); err != nil {
		t.Fatalf("payload 非法 json: %s", err)
	}
	joined := ""
	for _, p := range payload.Skip {
		joined += p + "\n"
	}
	for _, want := range []string{"password_new", "logout", "delete_?(user|account)"} {
		if !contains(joined, want) {
			t.Errorf("default 档案缺 %s", want)
		}
	}
	if contains(joined, "phpids") {
		t.Error("default 档案不应含设置类规则")
	}

	// strict：追加设置类
	resetState(true, true, "default")
	ObserveHTML("<title>Damn Vulnerable Web Application</title>")
	payload.Skip = nil
	if err := json.Unmarshal([]byte(FormSkipPayload()), &payload); err != nil {
		t.Fatalf("payload 非法 json: %s", err)
	}
	joined = ""
	for _, p := range payload.Skip {
		joined += p + "\n"
	}
	if !contains(joined, "phpids") {
		t.Error("strict 档案应追加设置类规则")
	}

	// disabled：空列表
	resetState(false, true, "default")
	payload.Skip = nil
	if err := json.Unmarshal([]byte(FormSkipPayload()), &payload); err != nil {
		t.Fatalf("payload 非法 json: %s", err)
	}
	if len(payload.Skip) != 0 {
		t.Errorf("disabled 应返回空列表，got %v", payload.Skip)
	}
}

// 表单 HTML 匹配语义核对：黑名单正则应命中 DVWA 的改密码/设置表单 outerHTML
func TestFormHTMLMatchSemantics(t *testing.T) {
	cases := []struct {
		name      string
		formHTML  string
		wantBlock bool
	}{
		{"DVWA csrf 改密码表单", `<form action="#" method="GET"><input type="password" name="password_new"><input type="password" name="password_conf"><input type="submit" value="Change"></form>`, true},
		{"DVWA security 设置表单", `<form action="#" method="post"><select name="security"><option value="low">low</option></select><input type="submit" value="Submit"></form>`, true},
		{"普通登录表单", `<form action="login.php" method="post"><input name="username"><input type="password" name="password"><input type="submit" value="Login"></form>`, false},
		{"登出表单", `<form action="/logout" method="post"><input type="submit" value="退出"></form>`, true},
		{"删除用户表单", `<form action="/admin/user" method="post"><input name="delete_user"><input type="submit" value="Delete"></form>`, true},
	}
	resetState(true, true, "default")
	ObserveHTML("<title>Damn Vulnerable Web Application (DVWA)</title>")
	payloadPatterns := struct {
		Skip []string `json:"skip"`
	}{}
	if err := json.Unmarshal([]byte(FormSkipPayload()), &payloadPatterns); err != nil {
		t.Fatalf("payload 非法 json: %s", err)
	}
	res := compileRes(payloadPatterns.Skip)
	for _, c := range cases {
		low := strings.ToLower(c.formHTML)
		hit := false
		for _, re := range res {
			if re.MatchString(low) {
				hit = true
				break
			}
		}
		if hit != c.wantBlock {
			t.Errorf("%s: block=%v, want %v", c.name, hit, c.wantBlock)
		}
	}
}
