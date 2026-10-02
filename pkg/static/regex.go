package static

import (
	"regexp"
)

// 让 chat.openai.com 帮我生成的
func findUrlMatch(content string) []string {
	regexStr := `(?i)\b((?:https?://|www\d{0,3}[.]|[a-z0-9.\-]+[.][a-z]{2,4}/)(?:[^\s()<>]+|\(([^\s()<>]+|(\([^\s()<>]+\)))*\))+(?:\(([^\s()<>]+|(\([^\s()<>]+\)))*\)|[^\s` + "`" + `!()\[\]{};:'".,<>?«»“”‘’]))`
	urlRegex := regexp.MustCompile(regexStr)
	urls := urlRegex.FindAllString(content, -1)
	return urls
}

// FindUrls 提取内容中的 URL，供其它包（如 pkg/extract）复用同一份正则。
func FindUrls(content string) []string {
	return findUrlMatch(content)
}

func MatchKeyExist(content []byte, regexStr string) bool {
	regex := regexp.MustCompile(regexStr)
	return regex.Match(content)
}
