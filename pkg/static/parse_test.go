package static

import "testing"

func TestHandlerUrl(t *testing.T) {
	currentUrl1 := "http://testphp.vulnweb.com/hpp/?pp=12"
	urlStr1 := "params.php?p=valid&pp=12"
	h1 := HandlerUrl(urlStr1, currentUrl1)
	if "http://testphp.vulnweb.com/hpp/params.php?p=valid&pp=12" != h1 {
		t.Errorf("handlerurl1  fail: %s", h1)
	}
}
