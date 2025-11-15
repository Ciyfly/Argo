package static

import (
	"fmt"
	"strings"
)

var NotFoundKeyWords = []string{
	"not found", "页面不存在", "<title>404", "missing", "does not exist",
}

// TODO: 优化页面相似度算法 匹配404页面
func Match404ResponsePage(body []byte) bool {
	regexStr := fmt.Sprintf(`(?i)(%s)`, strings.Join(NotFoundKeyWords, "|"))
	if MatchKeyExist(body, regexStr) {
		return true
	}
	trimmed := strings.TrimSpace(strings.ToLower(string(body)))
	if len(trimmed) == 0 {
		return false
	}
	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "error") {
		if strings.Contains(trimmed, "not found") || strings.Contains(trimmed, "missing") {
			return true
		}
	}
	return false
}
