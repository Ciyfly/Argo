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

func TestCanonicalizeURL_RejectsMetaLikeDirectives(t *testing.T) {
	tests := []struct {
		raw  string
		base string
	}{
		{"width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no", "http://10.199.0.134/"},
		{"IE=edge,chrome=1", "http://10.199.0.134/"},
	}

	for _, tt := range tests {
		if got, err := CanonicalizeURL(tt.raw, tt.base); err == nil {
			t.Fatalf("expected error for %q, got=%q", tt.raw, got)
		}
	}
}

func TestCanonicalizeURL_CollapseRepeatedStaticSegments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		base string
		want string
	}{
		{
			name: "static under /static/ should not become /static/static",
			raw:  "static/css",
			base: "http://10.199.0.134/static/",
			want: "http://10.199.0.134/static/css",
		},
		{
			name: "static under app subpath should keep prefix",
			raw:  "static/css",
			base: "http://10.199.0.134/qmallportal/static/",
			want: "http://10.199.0.134/qmallportal/static/css",
		},
		{
			name: "multiple repeats should collapse to single static",
			raw:  "static/static/static/css",
			base: "http://10.199.0.134/qmallportal/static/",
			want: "http://10.199.0.134/qmallportal/static/css",
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
