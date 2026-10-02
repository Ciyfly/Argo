package extract

import (
	"fmt"
	"regexp"
	"sync"
)

// 密钥泄漏检测：对文本响应体跑规则表（默认规则 + 配置追加规则）。
//
// 与 katana 的 titus（WASM 扫描器）路线不同：Argo 自身规则表即可覆盖
// 常见云厂商 token / 私钥 / JWT 等高价值格式，且无重依赖、无外联验证
// ——验证需要向密钥厂商发真实请求，涉及隐私，不做。

// SecretRule 单条密钥规则。GroupIdx 指定命中值取第几个捕获组（0 = 整个匹配）。
type SecretRule struct {
	Name     string
	Regex    string
	Severity string
	GroupIdx int
}

// SecretFinding 一条密钥命中。Value 只保留打码后的形式，完整明文不落任何输出。
type SecretFinding struct {
	URL         string
	Rule        string
	MaskedValue string
	Severity    string
}

type compiledSecretRule struct {
	name     string
	regex    *regexp.Regexp
	severity string
	// groupIdx 命中值取第几个捕获组，0 表示整个匹配
	groupIdx int
}

// maxMatchesPerRule 单规则单响应最多报告条数，防止一个错误页面刷屏
const maxMatchesPerRule = 5

// defaultSecretRules 默认规则：只收「格式特征强、误报低」的通用密钥形态。
var defaultSecretRules = []SecretRule{
	{Name: "AWS AccessKey", Regex: `AKIA[0-9A-Z]{16}`, Severity: "high"},
	{Name: "AWS SecretKey", Regex: `(?i)aws.{0,20}['"][0-9a-zA-Z/+]{40}['"]`, Severity: "high"},
	{Name: "GitHub Token", Regex: `gh[pousr]_[A-Za-z0-9]{36,255}`, Severity: "high"},
	{Name: "Slack Token", Regex: `xox[abprs]-[0-9A-Za-z-]{10,}`, Severity: "medium"},
	{Name: "Google API Key", Regex: `AIza[0-9A-Za-z\-_]{35}`, Severity: "medium"},
	{Name: "Stripe Live Key", Regex: `(?:sk|pk)_live_[0-9a-zA-Z]{24,}`, Severity: "high"},
	{Name: "Aliyun AccessKey", Regex: `LTAI[0-9A-Za-z]{12,20}`, Severity: "high"},
	{Name: "Private Key", Regex: `-----BEGIN (?:RSA |EC |OPENSSH |DSA |)?PRIVATE KEY-----`, Severity: "high"},
	{Name: "JWT", Regex: `eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`, Severity: "low"},
	// 密码赋值形态取捕获组（只要值不要整句）
	{Name: "Password Assignment", Regex: `(?i)(?:password|passwd|pwd)["']?\s*[:=]\s*["']([^"']{6,})["']`, Severity: "medium", GroupIdx: 1},
}

var (
	secretRulesMu sync.RWMutex
	secretRules   []compiledSecretRule
)

// InitSecretRules 编译默认规则 + 配置追加规则。正则非法返回错误，由调用方提示用户。
func InitSecretRules(extra []SecretRule) error {
	rules := make([]compiledSecretRule, 0, len(defaultSecretRules)+len(extra))
	for _, rule := range append(append([]SecretRule{}, defaultSecretRules...), extra...) {
		re, err := regexp.Compile(rule.Regex)
		if err != nil {
			return fmt.Errorf("secret rule %q: %s", rule.Name, err)
		}
		groupIdx := rule.GroupIdx
		if groupIdx > re.NumSubexp() {
			groupIdx = 0
		}
		rules = append(rules, compiledSecretRule{name: rule.Name, regex: re, severity: rule.Severity, groupIdx: groupIdx})
	}
	secretRulesMu.Lock()
	secretRules = rules
	secretRulesMu.Unlock()
	return nil
}

// ScanSecrets 扫描响应体，返回命中列表（可能为空）。
func ScanSecrets(url string, body []byte) []SecretFinding {
	secretRulesMu.RLock()
	rules := secretRules
	secretRulesMu.RUnlock()
	if len(rules) == 0 || len(body) == 0 {
		return nil
	}

	findings := make([]SecretFinding, 0)
	for _, rule := range rules {
		matches := rule.regex.FindAllStringSubmatch(string(body), maxMatchesPerRule)
		for _, match := range matches {
			value := match[0]
			if rule.groupIdx < len(match) && match[rule.groupIdx] != "" {
				value = match[rule.groupIdx]
			}
			findings = append(findings, SecretFinding{
				URL:         url,
				Rule:        rule.name,
				MaskedValue: maskSecret(value),
				Severity:    rule.severity,
			})
		}
	}
	return findings
}

// maskSecret 打码展示：保头 6 尾 4（短值保头 3），中间用 *** 代替。
// 完整明文不进入日志和结果文件。
func maskSecret(s string) string {
	if len(s) <= 10 {
		if len(s) <= 4 {
			return s[:1] + "***"
		}
		return s[:3] + "***"
	}
	return s[:6] + "***" + s[len(s)-4:]
}
