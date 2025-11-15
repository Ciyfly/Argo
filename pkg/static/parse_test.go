package static

import "testing"

func TestHandlerUrl(t *testing.T) {
	base := "http://testphp.vulnweb.com/hpp/?pp=12"
	tests := []struct {
		raw  string
		want string
	}{
		{"params.php?p=valid&pp=12", "http://testphp.vulnweb.com/hpp/params.php?p=valid&pp=12"},
		{"//static.example.com/a.js", "http://static.example.com/a.js"},
		{"cdn.example.com/assets/app.js", "http://cdn.example.com/assets/app.js"},
		{"../admin", "http://testphp.vulnweb.com/admin"},
		{"https://foo.com/path#section", "https://foo.com/path"},
		{"foo.com/path?z=1&&a=2", "http://foo.com/path?a=2&z=1"},
		{"&#39", ""},
		{"mailto:test@example.com", ""},
		{"javascript:void(0)", ""},
	}
	for _, tt := range tests {
		got := HandlerUrl(tt.raw, base)
		if got != tt.want {
			t.Errorf("HandlerUrl(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
