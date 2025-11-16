package utils

import (
	"regexp"
)

var (
	absoluteURLRegex = regexp.MustCompile(`https?://[^\s"'<>]+`)
	jsKeywordRegex   = regexp.MustCompile("(?i)(fetch|axios(?:\\.(?:get|post))?|open|location\\.(?:href|assign|replace)|router\\.push)\\s*\\(\\s*['\"\\x60]([^'\"\\x60]+)['\"\\x60]")
)

// MustCompileURLRegex returns a regex that matches absolute http/https URLs.
func MustCompileURLRegex() *regexp.Regexp {
	return absoluteURLRegex
}

// MustCompileJSKeywordRegex returns a regex that matches common JS navigation/fetch patterns.
func MustCompileJSKeywordRegex() *regexp.Regexp {
	return jsKeywordRegex
}

// UniqueStrings removes duplicates while preserving order.
func UniqueStrings(input []string) []string {
	if len(input) == 0 {
		return input
	}
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, val := range input {
		if val == "" {
			continue
		}
		if _, exists := seen[val]; exists {
			continue
		}
		seen[val] = struct{}{}
		result = append(result, val)
	}
	return result
}
