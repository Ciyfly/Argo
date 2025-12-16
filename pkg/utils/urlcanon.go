package utils

import (
	"errors"
	"html"
	"net/url"
	"strings"
)

var (
	ErrEmptyURL     = errors.New("url empty")
	ErrSchemeDenied = errors.New("scheme not allowed")
	ErrNoBase       = errors.New("relative url requires base")
	ErrNoHost       = errors.New("host missing")
)

// 常见“文件扩展名”，用于避免把 index.php / style.css 误判成裸域名。
// 说明：这里做的是经验性兜底，不追求覆盖所有后缀；主要目的是减少无效域名导致的 DNS/连接超时卡顿。
var commonFileExtensions = map[string]struct{}{
	"php":   {},
	"asp":   {},
	"aspx":  {},
	"jsp":   {},
	"cgi":   {},
	"html":  {},
	"htm":   {},
	"css":   {},
	"js":    {},
	"json":  {},
	"xml":   {},
	"txt":   {},
	"md":    {},
	"map":   {},
	"wasm":  {},
	"png":   {},
	"jpg":   {},
	"jpeg":  {},
	"gif":   {},
	"svg":   {},
	"ico":   {},
	"webp":  {},
	"bmp":   {},
	"swf":   {},
	"woff":  {},
	"woff2": {},
	"ttf":   {},
	"eot":   {},
	"otf":   {},
	"pdf":   {},
	"zip":   {},
	"rar":   {},
	"7z":    {},
	"tar":   {},
	"gz":    {},
	"bz2":   {},
	"xz":    {},
	"mp4":   {},
	"mp3":   {},
	"avi":   {},
	"mov":   {},
	"wmv":   {},
	"flv":   {},
	"webm":  {},
}

// CanonicalizeURL sanitizes, resolves and normalizes a URL relative to base (if provided).
// It returns an absolute http/https URL or an error describing why it was rejected.
func CanonicalizeURL(raw string, base string) (string, error) {
	clean := sanitizeURLCandidate(raw)
	if clean == "" {
		return "", ErrEmptyURL
	}
	if schemeRejected(clean) {
		return "", ErrSchemeDenied
	}

	var baseURL *url.URL
	if base != "" {
		if b, err := url.Parse(base); err == nil {
			if b.Scheme == "" {
				b.Scheme = "http"
			}
			baseURL = b
		}
	}

	candidateStr := clean
	switch {
	case strings.HasPrefix(candidateStr, "//"):
		if baseURL == nil {
			return "", ErrNoBase
		}
		candidateStr = baseURL.Scheme + ":" + candidateStr
	case looksLikeHostWithPath(candidateStr):
		if baseURL == nil {
			return "", ErrNoBase
		}
		candidateStr = baseURL.Scheme + "://" + candidateStr
	case looksLikeBareDomain(candidateStr):
		if baseURL == nil {
			return "", ErrNoBase
		}
		candidateStr = baseURL.Scheme + "://" + candidateStr
	}

	candidate, err := url.Parse(candidateStr)
	if err != nil {
		return "", err
	}
	if !candidate.IsAbs() {
		if baseURL == nil {
			return "", ErrNoBase
		}
		candidate = baseURL.ResolveReference(candidate)
	}
	if candidate.Scheme == "" {
		if baseURL != nil && baseURL.Scheme != "" {
			candidate.Scheme = baseURL.Scheme
		} else {
			candidate.Scheme = "http"
		}
	}
	if !isAllowedScheme(candidate.Scheme) {
		return "", ErrSchemeDenied
	}
	if candidate.Host == "" {
		if baseURL != nil {
			candidate.Host = baseURL.Host
		}
	}
	if candidate.Host == "" {
		return "", ErrNoHost
	}
	candidate.Fragment = ""
	if rawQuery := candidate.Query(); len(rawQuery) > 0 {
		candidate.RawQuery = rawQuery.Encode()
	}
	return candidate.String(), nil
}

func sanitizeURLCandidate(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ""
	}
	clean = html.UnescapeString(clean)
	clean = strings.TrimSpace(clean)
	clean = strings.Trim(clean, "\"'")
	clean = strings.TrimSpace(clean)
	if clean == "" || clean == "#" {
		return ""
	}
	return clean
}

func schemeRejected(candidate string) bool {
	lower := strings.ToLower(strings.TrimSpace(candidate))
	if lower == "" {
		return true
	}
	disallowed := []string{"javascript:", "mailto:", "tel:", "sms:", "data:", "blob:", "intent:"}
	for _, prefix := range disallowed {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func looksLikeBareDomain(candidate string) bool {
	if candidate == "" {
		return false
	}
	if strings.Contains(candidate, "://") || strings.HasPrefix(candidate, "//") {
		return false
	}
	if strings.ContainsAny(candidate, " \t\n") {
		return false
	}
	if strings.ContainsAny(candidate, "?=&") {
		return false
	}
	trimmed := strings.Trim(candidate, "/")
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return false
	}
	if !strings.Contains(trimmed, ".") {
		return false
	}
	if strings.HasPrefix(trimmed, ".") {
		return false
	}

	parsed, err := url.Parse("http://" + trimmed)
	if err != nil {
		return false
	}

	// 关键修复：避免把 index.php / style.css 这类相对资源名误判为裸域名。
	host := strings.ToLower(parsed.Hostname())
	if host != "" {
		if idx := strings.LastIndex(host, "."); idx > 0 && idx < len(host)-1 {
			if _, ok := commonFileExtensions[host[idx+1:]]; ok {
				return false
			}
		}
	}

	return true
}

func looksLikeHostWithPath(candidate string) bool {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, ".") {
		return false
	}
	if strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "//") {
		return false
	}
	if strings.ContainsAny(trimmed, " \t") {
		return false
	}
	if strings.ContainsAny(trimmed, "?=&") && !strings.Contains(trimmed, "/") {
		return false
	}
	firstSlash := strings.Index(trimmed, "/")
	if firstSlash <= 0 {
		return false
	}
	hostPart := trimmed[:firstSlash]
	if !strings.Contains(hostPart, ".") {
		return false
	}
	if strings.HasPrefix(hostPart, ".") || strings.HasSuffix(hostPart, ".") {
		return false
	}

	// 关键修复：避免把 foo.php/bar 这类“相对路径(带点)”误判成 host/path。
	if parsed, err := url.Parse("http://" + hostPart); err == nil {
		host := strings.ToLower(parsed.Hostname())
		if host != "" {
			if idx := strings.LastIndex(host, "."); idx > 0 && idx < len(host)-1 {
				if _, ok := commonFileExtensions[host[idx+1:]]; ok {
					return false
				}
			}
		}
	}

	return true
}

func isAllowedScheme(s string) bool {
	switch strings.ToLower(s) {
	case "http", "https":
		return true
	default:
		return false
	}
}
