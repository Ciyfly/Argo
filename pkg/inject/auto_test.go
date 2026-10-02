package inject

import (
	"strings"
	"testing"
)

// 回归测试：自动填表必须通过原生 value setter 给 input 赋值。
//
// 历史上用的是 node.textContent / node.nodeValue / node.setRangeText，
// 这三个对 input 都改不了 value（textContent 只影响元素子节点，
// setRangeText 只对选中文本生效），所以自动填表实际什么都没填进去。
func TestAutoFillUsesNativeValueSetter(t *testing.T) {
	for _, bad := range []string{
		"textContent =",
		"nodeValue =",
		"setRangeText(",
	} {
		if strings.Contains(autoFillJS, bad) {
			t.Errorf("填表 JS 里仍有无效的赋值方式: %s", bad)
		}
	}
	if !strings.Contains(autoFillJS, "Object.getOwnPropertyDescriptor") {
		t.Errorf("缺少通过原型 value setter 赋值的实现，框架类页面的输入框不会被识别")
	}
	if !strings.Contains(autoFillJS, `new Event("input"`) {
		t.Errorf("赋值后没有派发 input 事件，依赖事件监听的页面感知不到输入")
	}
	if !strings.Contains(autoFillJS, `new Event("change"`) {
		t.Errorf("赋值后没有派发 change 事件")
	}
}

// 回归测试：四种输入框类型都要被覆盖到。
func TestAutoFillCoversInputTypes(t *testing.T) {
	for _, typ := range []string{"text", "password", "email", "tel"} {
		if !strings.Contains(autoFillJS, `t === "`+typ+`"`) {
			t.Errorf("填表 JS 没有处理 type=%s 的输入框", typ)
		}
	}
}

// 回归测试：可点击元素的选择器必须覆盖交互型元素。
//
// 历史实现只处理 <a>、<button>、<input>，导致 select、SVG 热点、
// ARIA role 容器上的入口全部漏掉。
func TestClickableSelectorCoversInteractiveElements(t *testing.T) {
	for _, sel := range []string{
		"button",
		"input[type=button]",
		"input[type=submit]",
		"select",
		"[onclick]",
		"svg",
		"[role=button]",
		"[role=tab]",
		"[role=menuitem]",
		"summary",
	} {
		if !strings.Contains(autoClickableSelector, sel) {
			t.Errorf("可点击选择器缺少 %q", sel)
		}
	}
}

// 回归测试：点击前必须重新查询 DOM，不能缓存节点列表。
//
// 历史实现把元素收集到数组后逐个 shift，点击改变页面结构后引用失效，
// 且收集循环结束后函数就返回，点击新产生的链接从未被读取。
func TestClickReQueriesDOM(t *testing.T) {
	if !strings.Contains(clickBySigJS, "querySelectorAll") {
		t.Errorf("clickBySigJS 必须每次重新查询 DOM，而不是使用缓存的节点列表")
	}
}

// 回归测试：点击必须按签名定位，不能用下标。
//
// 点击会往 DOM 里插入新按钮（确认按钮、菜单项），
// 后面的元素下标会整体错位，按下标点会点到错的元素上。
func TestClickLocatesBySignature(t *testing.T) {
	if !strings.Contains(clickBySigJS, "targetSig") {
		t.Errorf("clickBySigJS 应按签名定位元素，避免 DOM 插入新节点后下标错位")
	}
}

// 回归测试：点击 <a> 前必须摘掉 href，避免整页导航打断后续交互。
func TestClickRemovesHrefToAvoidNavigation(t *testing.T) {
	if !strings.Contains(clickBySigJS, "removeAttribute(\"href\")") {
		t.Errorf("clickBySigJS 应在点击 <a> 前移除 href，避免导航导致执行上下文失效")
	}
}

// 回归测试：点击前必须拆掉 formaction，并把 submit 按钮降级为普通按钮。
//
// 靶场里存在 <button formaction="..." type="submit">，点一下就会提交表单并整页跳转，
// 之后所有点击都落在错误页面上（实测导致 22 个元素全部点空）。
func TestClickNeutralizesFormSubmit(t *testing.T) {
	if !strings.Contains(clickBySigJS, "removeAttribute(\"formaction\")") {
		t.Errorf("clickBySigJS 应移除 formaction，避免表单提交导致整页导航")
	}
	if !strings.Contains(clickBySigJS, `setAttribute("type", "button")`) {
		t.Errorf("clickBySigJS 应把 submit 按钮降级为普通按钮")
	}
}

// 回归测试：链接扫描必须返回绝对地址，且跳过 javascript: 伪协议。
func TestScanLinksFiltersAndResolves(t *testing.T) {
	if !strings.Contains(scanLinksJS, `indexOf("javascript:") === 0`) {
		t.Errorf("scanLinksJS 应跳过 javascript: 伪协议链接")
	}
	if !strings.Contains(scanLinksJS, "a.href") {
		t.Errorf("scanLinksJS 应使用浏览器解析后的绝对地址 a.href")
	}
}
