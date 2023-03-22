package static

import (
	"fmt"
	"strings"
)

var NotFoundKeyWords = []string{
	"not found", "页面不存在",
}

// TODO: 优化页面相似度算法 匹配404页面
func Match404ResponsePage(body []byte) bool {
	regexStr := fmt.Sprintf(`(?i)(%s)`, strings.Join(NotFoundKeyWords, "|"))
	return MatchKeyExist(body, regexStr)
}
