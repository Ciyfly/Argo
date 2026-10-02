package extract

import (
	"strings"
	"testing"
)

func mustInitRules(t *testing.T, extra []SecretRule) {
	t.Helper()
	if err := InitSecretRules(extra); err != nil {
		t.Fatalf("InitSecretRules err: %s", err)
	}
}

func TestScanSecretsDefaults(t *testing.T) {
	mustInitRules(t, nil)

	cases := []struct {
		body string
		rule string
	}{
		{`key = "AKIAIOSFODNN7EXAMPLE"`, "AWS AccessKey"},
		{`token: "ghp_0123456789abcdefghijklmnopqrstuvwxyzABCDEF"`, "GitHub Token"},
		{`var k = "xoxb-123456789012-123456789012-abcdefabcdef"`, "Slack Token"},
		{`gkey = "AIzaSyA1234567890abcdefghijklmnopqrstuv"`, "Google API Key"},
		{`sk = "sk_live_0123456789abcdefghijklmnopqrstuv"`, "Stripe Live Key"},
		{`ali = "LTAI5tAbCdEfGhIjKlMnOpQrs"`, "Aliyun AccessKey"},
		{"-----BEGIN RSA PRIVATE KEY-----", "Private Key"},
		{`jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N65IqZ7pWfS"`, "JWT"},
		{`password: "admin123456"`, "Password Assignment"},
	}
	for _, c := range cases {
		findings := ScanSecrets("https://a.com/x", []byte(c.body))
		found := false
		for _, f := range findings {
			if f.Rule == c.rule {
				found = true
			}
		}
		if !found {
			t.Errorf("应当检出 %s，body: %s，实际: %+v", c.rule, c.body, findings)
		}
	}
}

func TestScanSecretsNoFalsePositiveOnNormalText(t *testing.T) {
	mustInitRules(t, nil)
	findings := ScanSecrets("https://a.com/x", []byte("normal page content with words like password and key but no real secret"))
	if len(findings) != 0 {
		t.Errorf("普通文本不应检出密钥，实际: %+v", findings)
	}
}

func TestScanSecretsMaskedValue(t *testing.T) {
	mustInitRules(t, nil)
	findings := ScanSecrets("https://a.com/x", []byte(`k = "AKIAIOSFODNN7EXAMPLE"`))
	if len(findings) != 1 {
		t.Fatalf("应命中 1 条，实际 %d", len(findings))
	}
	v := findings[0].MaskedValue
	if strings.Contains(v, "IOSFODNN7") {
		t.Errorf("打码值不应包含明文中段: %s", v)
	}
	if !strings.HasSuffix(v, "MPLE") {
		t.Errorf("打码值应保留尾 4 位便于人工核对: %s", v)
	}
}

func TestScanSecretsCustomRule(t *testing.T) {
	mustInitRules(t, []SecretRule{{Name: "InnerToken", Regex: `inner_[0-9a-z]{20}`, Severity: "high"}})
	findings := ScanSecrets("https://a.com/x", []byte(`t = "inner_0123456789abcdefghij"`))
	found := false
	for _, f := range findings {
		if f.Rule == "InnerToken" {
			found = true
		}
	}
	if !found {
		t.Errorf("自定义规则应生效，实际: %+v", findings)
	}
}

func TestInitSecretRulesInvalidRegex(t *testing.T) {
	err := InitSecretRules([]SecretRule{{Name: "bad", Regex: `[invalid`}})
	if err == nil {
		t.Fatalf("非法正则应返回错误")
	}
}

func TestScanSecretsEmpty(t *testing.T) {
	mustInitRules(t, nil)
	if got := ScanSecrets("https://a.com/x", nil); got != nil {
		t.Errorf("空 body 不应检出: %+v", got)
	}
}
