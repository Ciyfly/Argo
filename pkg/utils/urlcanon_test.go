package utils

import "testing"

// 这些用例来自实际爬虫日志：相对资源名(如 index.php / style.css)不应被误判为裸域名。
func TestCanonicalizeURL_RelativeFileNotBareDomain(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		base string
		want string
	}{
		{
			name: "index.php should resolve to base host",
			raw:  "index.php",
			base: "http://testphp.vulnweb.com/",
			want: "http://testphp.vulnweb.com/index.php",
		},
		{
			name: "style.css should resolve to base host",
			raw:  "style.css",
			base: "http://testphp.vulnweb.com/",
			want: "http://testphp.vulnweb.com/style.css",
		},
		{
			name: "showxml.php should resolve to current directory",
			raw:  "showxml.php",
			base: "http://testphp.vulnweb.com/AJAX/index.php",
			want: "http://testphp.vulnweb.com/AJAX/showxml.php",
		},
		{
			name: "bare domain should stay bare domain",
			raw:  "foo.com",
			base: "http://testphp.vulnweb.com/",
			want: "http://foo.com",
		},
		{
			name: "host with path should be treated as absolute host",
			raw:  "cdn.example.com/assets/app.js",
			base: "http://testphp.vulnweb.com/",
			want: "http://cdn.example.com/assets/app.js",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalizeURL(tt.raw, tt.base)
			if err != nil {
				t.Fatalf("CanonicalizeURL(%q, %q) unexpected error: %v", tt.raw, tt.base, err)
			}
			if got != tt.want {
				t.Fatalf("CanonicalizeURL(%q, %q) = %q, want %q", tt.raw, tt.base, got, tt.want)
			}
		})
	}
}
