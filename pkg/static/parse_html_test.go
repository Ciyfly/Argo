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

func TestParseHtml_BaseHrefAvoidsPathStacking(t *testing.T) {
	html := `
<html>
  <head>
    <base href="/">
    <meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no">
    <meta http-equiv="X-UA-Compatible" content="IE=edge,chrome=1">
  </head>
  <body>
    <link rel="stylesheet" href="static/css">
    <script src=".1764567702477.js"></script>
    <a href="static">static</a>
  </body>
</html>`

	// 模拟 SPA fallback：在 /static/ 下拿到相同的 index.html，若不识别 base href，则会解析成 /static/static/*
	base := "http://10.199.0.134/static/"
	urls := ParseHtml(html, base)

	mustContain := []string{
		"http://10.199.0.134/static/css",
		"http://10.199.0.134/.1764567702477.js",
		"http://10.199.0.134/static",
	}
	for _, want := range mustContain {
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

	// 关键断言：不应出现 /static/static/... 的叠加
	for _, u := range urls {
		if strings.Contains(u, "/static/static/") || strings.HasSuffix(u, "/static/static") {
			t.Fatalf("base href should avoid path stacking, got: %q, urls=%v", u, urls)
		}
	}
}
