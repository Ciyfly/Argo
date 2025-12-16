package static

import (
	"strings"
	"testing"
)

func TestParseHtml_MetaCharsetContentNotTreatedAsURL(t *testing.T) {
	html := `
<html>
  <head>
    <meta http-equiv="Content-Type" content="text/html; charset=iso-8859-2">
  </head>
  <body>
    <a href="index.php">home</a>
  </body>
</html>`

	base := "http://testphp.vulnweb.com/"
	urls := ParseHtml(html, base)

	for _, u := range urls {
		if strings.Contains(u, "text/html") || strings.Contains(u, "charset=") {
			t.Fatalf("meta charset content should not be treated as URL, got: %q", u)
		}
	}

	// 同时验证相对链接仍能正确解析
	want := "http://testphp.vulnweb.com/index.php"
	found := false
	for _, u := range urls {
		if u == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q in parsed urls, got=%v", want, urls)
	}
}
