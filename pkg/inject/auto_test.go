package inject

import (
	"os"
	"strings"
	"testing"
)

// 回归测试：自动填表必须通过原生 value setter 给 input 赋值。
//
// 历史实现用 node.textContent / node.nodeValue / node.setRangeText，
// 这三个对 input 都改不了 value（textContent 只影响元素子节点，
// setRangeText 只对选中文本生效），所以自动填表实际什么都没填进去。
func TestAutoFillUsesNativeValueSetter(t *testing.T) {
	for _, bad := range []string{"textContent =", "nodeValue =", "setRangeText("} {
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
	if !strings.Contains(clickBySigJS, `removeAttribute("href")`) {
		t.Errorf("clickBySigJS 应在点击 <a> 前移除 href，避免导航导致执行上下文失效")
	}
}

// 回归测试：点击前必须拆掉 formaction，并把 submit 按钮降级为普通按钮。
//
// 靶场里存在 <button formaction="..." type="submit">，点一下就会提交表单
// 并整页跳转，之后所有点击都落在错误页面上（实测导致 22 个元素全部点空）。
func TestClickNeutralizesFormSubmit(t *testing.T) {
	if !strings.Contains(clickBySigJS, `removeAttribute("formaction")`) {
		t.Errorf("clickBySigJS 应移除 formaction，避免表单提交导致整页导航")
	}
	if !strings.Contains(clickBySigJS, `setAttribute("type", "button")`) {
		t.Errorf("clickBySigJS 应把 submit 按钮降级为普通按钮")
	}
}

// 回归测试：链接扫描必须返回绝对地址，且跳过伪协议。
func TestScanLinksFiltersAndResolves(t *testing.T) {
	for _, js := range []string{scanLinksJS, scanLinksDeepJS} {
		if !strings.Contains(js, `indexOf("javascript:") === 0`) {
			t.Errorf("链接扫描应跳过 javascript: 伪协议链接")
		}
		if !strings.Contains(js, "new URL(") {
			t.Errorf("链接扫描应用 URL 解析器把相对路径转成绝对地址")
		}
		if !strings.Contains(js, "document.baseURI") {
			t.Errorf("链接扫描应以 document.baseURI 为基准解析相对路径")
		}
	}
}

// 回归测试：链接发现必须是「通用属性值提取」，而不是属性名白名单。
//
// 历史教训：最初只扫 <a href>，后来改成白名单（data-href/data-url/...）。
// 白名单是特解——换成别的属性名就失效。正确做法是遍历所有元素的
// 所有属性值，凡看起来像 URL 的都收，对未知框架的自定义字段同样有效。
//
// 实现采用分层设计：
//
//	热路径（每次点击后）只扫规范定义的标准属性，保证便宜；
//	冷路径（每页开头/结尾）遍历全部元素全部属性值 + shadow DOM。
func TestScanLinksUsesGenericExtraction(t *testing.T) {
	// 两层都不允许出现属性名白名单
	for _, bad := range []string{`"data-href"`, `"data-url"`, `"data-src"`} {
		if strings.Contains(scanLinksJS, bad) || strings.Contains(scanLinksDeepJS, bad) {
			t.Errorf("scanLinks 仍在使用属性名白名单（特解），应改为通用属性值提取: %s", bad)
		}
	}
	// 冷路径必须遍历所有元素的属性
	if !strings.Contains(scanLinksDeepJS, "el.attributes") {
		t.Errorf("scanLinksDeepJS 应遍历元素属性（通用提取），而不是枚举属性名")
	}
	// 冷路径必须过滤高噪声属性，避免误报
	if !strings.Contains(scanLinksDeepJS, `"class"`) || !strings.Contains(scanLinksDeepJS, `"style"`) {
		t.Errorf("scanLinksDeepJS 应跳过 class/style 等高噪声属性，减少误报")
	}
	// 冷路径必须有 URL 形态判断，避免把普通文本当链接
	if !strings.Contains(scanLinksDeepJS, "looksLikeURL") {
		t.Errorf("scanLinksDeepJS 应有 URL 形态判断，避免把普通文本/数字当链接")
	}
	// 冷路径必须覆盖 shadow DOM
	if !strings.Contains(scanLinksDeepJS, "shadowRoot") {
		t.Errorf("scanLinksDeepJS 应递归进入 shadow DOM")
	}
	// 热路径必须保持便宜：不允许做全元素遍历
	if strings.Contains(scanLinksJS, `querySelectorAll("*")`) {
		t.Errorf("scanLinksJS 是点击热路径，不应做全元素遍历（会拖慢交互节奏，导致链路中段丢失）")
	}
}

// 回归测试：冷路径全量扫描必须接入主流程（开头和结尾各一次）。
// 之前实现过深扫却忘了调用，shadow/data-* 的链接全部白扫。
func TestDeepScanIsWiredIn(t *testing.T) {
	src, err := os.ReadFile("auto.go")
	if err != nil {
		t.Skipf("读不到 auto.go，跳过: %s", err)
	}
	// 定义是 "collectDeep := func() {"，不含 "collectDeep()"，
	// 所以这里数出来的是纯调用次数：基线 1 次 + 收尾 1 次 = 2 次
	if calls := strings.Count(string(src), "collectDeep()"); calls < 2 {
		t.Errorf("collectDeep() 应在主流程开头（基线）和结尾（收尾）各调用一次，实际出现 %d 次", calls)
	}
	// 表单提交必须接入主流程（否则 POST 类入口拿不到）
	if !strings.Contains(string(src), "submitFormsJS") {
		t.Errorf("auto.go 应调用 submitFormsJS（POST 类入口必须真正提交表单才能被发现）")
	}
}
