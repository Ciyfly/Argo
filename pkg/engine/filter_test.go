package engine

import "testing"

func TestFilterStatic(t *testing.T) {
	InitFilter()
	target1 := "http://testphp.vulnweb.com/style.css"
	target2 := "http://testphp.vulnweb.com/AJAX/infoartist.php?id=.css"
	target3 := "http://testphp.vulnweb.com/AJAX/styles.css#2378123687"
	if !filterStatic(target1) {
		t.Errorf("filterStatic fail: %s", target1)
	}
	if filterStatic(target2) {
		t.Errorf("filterStatic fail: %s", target1)
	}
	if !filterStatic(target3) {
		t.Errorf("filterStatic fail: %s", target1)
	}
}
