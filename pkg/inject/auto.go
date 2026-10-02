package inject

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/static"
	"argo/pkg/utils"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

// 可点击元素的选择器。
//
// 除了 <button> / <input>，还覆盖 select、带 onclick 的元素、SVG 热点，
// 以及用 ARIA role 标成按钮/标签页/菜单项的容器——SPA 里大量入口是这些。
const autoClickableSelector = "a[href^='javascript'], button, input[type=button], input[type=submit], select, [onclick], svg, [role=button], [role=tab], [role=menuitem], summary"

// autoFillJS 给页面上的输入框填默认账号。
//
// 必须走原生 value setter 并派发 input/change：
// 直接写 textContent/nodeValue 对 input 无效，
// 而 React/Vue 这类框架靠覆写 value setter 感知输入。
var autoFillJS = `(username, password, email, phone) => {
	function setValue(node, value) {
		const proto = node.tagName === "TEXTAREA" ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
		const descriptor = Object.getOwnPropertyDescriptor(proto, "value");
		if (descriptor && descriptor.set) {
			descriptor.set.call(node, value);
		} else {
			node.value = value;
		}
		node.dispatchEvent(new Event("input", {bubbles: true}));
		node.dispatchEvent(new Event("change", {bubbles: true}));
	}
	document.querySelectorAll("input, textarea").forEach(function (node) {
		const t = (node.type || "text").toLowerCase();
		if (t === "text") { setValue(node, username); }
		else if (t === "password") { setValue(node, password); }
		else if (t === "email") { setValue(node, email); }
		else if (t === "tel") { setValue(node, phone); }
	});
	return true;
}`

// scanLinksJS 扫描当前 DOM 里所有可访问链接，返回绝对地址。
// 用浏览器解析后的 a.href，避免页面跳转后相对路径算错。
var scanLinksJS = `() => {
	const out = [];
	document.querySelectorAll("a[href], area[href]").forEach(function (a) {
		const raw = a.getAttribute("href");
		if (!raw || raw === "#" || raw.indexOf("javascript:") === 0) { return; }
		const abs = a.href;
		if (abs && abs.indexOf("http") === 0) { out.push(abs); }
	});
	return out;
}`

// listClickableJS 列出可点击元素，附带稳定签名与是否应跳过。
//
// 签名用于跨轮次识别「这个元素已经点过了」：
// 每轮重新列举时列表里会包含旧按钮，不排重的话预算全浪费在重复点击上。
var listClickableJS = fmt.Sprintf(`(filters) => {
	const nodes = Array.prototype.slice.call(document.querySelectorAll(%s));
	const low = (filters || []).map(function (f) { return String(f).toLowerCase(); });
	const counter = {};
	return nodes.map(function (el, i) {
		let html = "";
		try { html = (el.outerHTML || "").toLowerCase(); } catch (e) {}
		let skip = false;
		for (let k = 0; k < low.length; k++) {
			if (low[k] && html.indexOf(low[k]) >= 0) { skip = true; break; }
		}
		// 稳定签名：标签 + id + 归一化文本。
		//
		// 文本必须归一化：按钮文案会随交互变化
		// （如「解锁下一步（0/3）」点一下变成「（1/3）」），
		// 直接拿原文会让同一个按钮每点一次都变成「新元素」。
		// 去掉数字后这两种写法归一成同一个签名。
		//
		// 而只靠 tag+id 也不行：页面里大量按钮没有 id，
		// 动态生成的按钮会和已有的无 id 按钮撞签名，被误判为已点过。
		let text = "";
		try { text = (el.textContent || "").replace(/[0-9]+/g, "#").trim().slice(0, 24); } catch (e) {}
		const base = el.tagName + "#" + (el.id || "") + "|" + text;
		counter[base] = (counter[base] || 0) + 1;
		return { index: i, skip: skip, tag: el.tagName, id: el.id || "",
		         sig: base + "|" + counter[base] };
	});
}`, fmt.Sprintf("%q", autoClickableSelector))

// clickBySigJS 按签名点击元素。
//
// 用签名而不是下标定位：点击会在 DOM 里插入新按钮
// （「确认后展开」生成的确认按钮、展开菜单生成的菜单项），
// 后面的元素下标会整体错位，按下标点会点到错的元素上。
//
// 点击前拆掉会造成整页导航的属性：
//   - <a href> ：直接跳转
//   - <button formaction> ：提交表单并跳转（靶场里真实存在这种元素，
//     一旦触发，后面所有点击都会落在错误页面上）
var clickBySigJS = fmt.Sprintf(`(targetSig) => {
	const nodes = Array.prototype.slice.call(document.querySelectorAll(%s));
	const counter = {};
	let el = null;
	for (let i = 0; i < nodes.length; i++) {
		let text = "";
		try { text = (nodes[i].textContent || "").replace(/[0-9]+/g, "#").trim().slice(0, 24); } catch (e) {}
		const base = nodes[i].tagName + "#" + (nodes[i].id || "") + "|" + text;
		counter[base] = (counter[base] || 0) + 1;
		if (base + "|" + counter[base] === targetSig) { el = nodes[i]; break; }
	}
	if (!el) { return false; }
	if (el.tagName === "A" && el.hasAttribute("href")) {
		el.setAttribute("data-argo-href", el.getAttribute("href"));
		el.removeAttribute("href");
	}
	if (el.hasAttribute && el.hasAttribute("formaction")) {
		el.setAttribute("data-argo-formaction", el.getAttribute("formaction"));
		el.removeAttribute("formaction");
	}
	// 表单提交类按钮也改成普通按钮，避免整页刷新
	if (el.tagName === "BUTTON" && (el.getAttribute("type") || "").toLowerCase() === "submit") {
		el.setAttribute("type", "button");
	}
	try { el.scrollIntoView({ block: "center" }); } catch (e) {}
	try { el.click(); } catch (e) { return false; }
	return true;
}`, fmt.Sprintf("%q", autoClickableSelector))

// dispatchEventsJS 向页面派发非 click 的交互事件。
//
// 很多入口挂在 contextmenu / dblclick / change / keydown 上，
// 只发 click 永远触发不到它们。select 变更会把目标 URL 写进 value，
// 所以这里一并把 value 收回来当链接用。
var dispatchEventsJS = `(shortcutKey) => {
	const found = [];
	function safe(fn) { try { fn(); } catch (e) {} }
	function push(v) { if (v && String(v).indexOf("http") === 0) { found.push(String(v)); } }

	// 右键菜单：常见于弹出隐藏操作入口
	safe(function () {
		document.querySelectorAll("body, [id*=ctx], [class*=ctx], [class*=context], [id*=menu], [class*=menu]").forEach(function (el) {
			safe(function () {
				el.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, view: window }));
			});
		});
	});

	// 双击
	safe(function () {
		document.querySelectorAll("button, [role=button], [ondblclick]").forEach(function (el) {
			safe(function () {
				el.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, view: window }));
			});
		});
	});

	// 下拉框：选中每一项并派发 change/input，
	// 有些实现把跳转地址直接写在 option.value 里
	safe(function () {
		document.querySelectorAll("select").forEach(function (sel) {
			for (let i = 0; i < sel.options.length; i++) {
				safe(function () {
					sel.selectedIndex = i;
					sel.dispatchEvent(new Event("input", { bubbles: true }));
					sel.dispatchEvent(new Event("change", { bubbles: true }));
				});
				push(sel.value);
				push(sel.options[i] && sel.options[i].value);
			}
		});
	});

	// 键盘快捷键（默认 Ctrl+Shift+S）
	safe(function () {
		const keys = shortcutKey ? String(shortcutKey).split("+") : ["Ctrl", "Shift", "S"];
		const last = keys[keys.length - 1];
		const ev = new KeyboardEvent("keydown", {
			key: last, code: "Key" + last.toUpperCase(),
			ctrlKey: keys.indexOf("Ctrl") >= 0,
			shiftKey: keys.indexOf("Shift") >= 0,
			altKey: keys.indexOf("Alt") >= 0,
			bubbles: true, cancelable: true
		});
		document.dispatchEvent(ev);
		window.dispatchEvent(ev);
	});

	return found;
}`

// armMutationJS / checkMutationJS 判断点击是否真的让页面发生了变化。
var armMutationJS = `() => {
	window.__argoMutated = false;
	if (window.__argoObserver) { try { window.__argoObserver.disconnect(); } catch (e) {} }
	try {
		window.__argoObserver = new MutationObserver(function () { window.__argoMutated = true; });
		window.__argoObserver.observe(document.documentElement, {
			subtree: true, childList: true, attributes: true, characterData: true
		});
	} catch (e) {}
	return true;
}`

var checkMutationJS = `() => !!window.__argoMutated`

// currentURL 读取页面当前地址，失败返回空串。
func currentURL(page *rod.Page) string {
	info, err := utils.GetPageInfoByPage(page)
	if err != nil {
		return ""
	}
	return info.URL
}

// stripFragment 去掉 URL 的 #fragment 部分。
//
// hash 变化不算真正的页面导航：页面 DOM 不会被重建，
// 之前点开的内容（展开的列表、生成的按钮）都还在。
// 如果把它当成导航去 backToStart，反而会把已展开的状态全部重置掉。
func stripFragment(u string) string {
	if i := strings.Index(u, "#"); i >= 0 {
		return u[:i]
	}
	return u
}

// navigatedAway 判断是否真的跳到了另一个页面（忽略 hash 变化）。
func navigatedAway(cur, start string) bool {
	if cur == "" || start == "" {
		return false
	}
	return stripFragment(cur) != stripFragment(start)
}

// backToStart 回到起点页面。
//
// 点击可能把页面导航到别处（表单提交、meta refresh、hash 跳转），
// 不回来的话后续点击全部落在错误的页面上，等于白点。
func backToStart(page *rod.Page, startURL string) {
	if startURL == "" {
		return
	}
	if err := page.Timeout(10 * time.Second).Navigate(startURL); err != nil {
		log.Logger.Debugf("auto back to start err: %s", err)
		return
	}
	if err := page.Timeout(10 * time.Second).WaitLoad(); err != nil {
		log.Logger.Debugf("auto back to start wait err: %s", err)
	}
	time.Sleep(150 * time.Millisecond)
}

// settleAfterClick 等页面响应点击。
//
// 轮询 DOM 是否发生变化：变了就再给一小段时间渲染完，
// 没变就最多等到 maxWait。这样有反应的按钮快、无反应的按钮也不至于拖死。
// 返回 DOM 是否真的发生了变化，供调用方判断要不要继续重复点。
func settleAfterClick(page *rod.Page, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		res, err := page.Eval(checkMutationJS)
		if err != nil {
			// 执行上下文失效，说明页面已经导航走了
			return true
		}
		if res != nil && res.Value.Bool() {
			time.Sleep(80 * time.Millisecond)
			return true
		}
	}
	return false
}

// Auto 在页面上做自动化交互，返回发现的 URL。
//
// 采用「Go 驱动」而不是把整套流程写进一个 JS 函数：
// 点击可能触发页面导航，一旦导航发生，浏览器里的 JS 执行上下文立刻失效，
// 把所有点击和等待塞在一个 JS 里必然中途崩掉（历史实现正是如此，
// 且收集完静态链接就 return，点击新产生的链接从未被读取）。
// 所以这里 JS 只做单步动作，循环、重扫、导航恢复都由 Go 侧控制。
func Auto(page *rod.Page) []string {
	info, err := utils.GetPageInfoByPage(page)
	if err != nil {
		return nil
	}
	startURL := info.URL
	log.Logger.Debugf("run auto js %s", startURL)

	cfg := conf.GlobalConfig
	// maxSettle 是单个元素等页面反应的上限。
	// 默认取 Slow，但设上限避免个别元素把预算吃干。
	maxSettle := time.Duration(cfg.AutoConf.Slow) * time.Millisecond
	if maxSettle <= 0 {
		maxSettle = time.Second
	}
	// 等待上限要压得够低。
	//
	// 实测（23 个可点击元素的首页）：
	//   每次等 800ms —— 只点完 19/23 就超预算，后面的元素根本轮不到；
	//   每次等 250ms —— 23/23 全部点到，耗时从 15.3s 降到 5.8s。
	// 因为等待是「轮询 DOM 是否变化」，有反应的元素会立即返回，
	// 所以压低上限不会漏掉真正有反应的入口。
	if maxSettle > 250*time.Millisecond {
		maxSettle = 250 * time.Millisecond
	}
	// 交互预算。
	//
	// 不能只按单个 tab 的超时来算：一个页面上几十个可交互元素，
	// 预算太小会导致后面的元素根本轮不到（实测小预算时 multi-step 一条都拿不到）。
	// 这里给一个下限，保证交互有机会跑完。
	budget := time.Duration(cfg.BrowserConf.TabTimeout) * time.Second
	if budget < 12*time.Second {
		budget = 12 * time.Second
	}
	deadline := time.Now().Add(budget)

	hrefList := []string{}
	seen := map[string]bool{}
	collect := func() {
		res, err := page.Eval(scanLinksJS)
		if err != nil {
			return
		}
		for _, v := range res.Value.Arr() {
			h := v.Str()
			if h == "" || seen[h] {
				continue
			}
			seen[h] = true
			hrefList = append(hrefList, h)
		}
	}

	// 1. 先填表单，让需要登录的页面进入登录态
	if _, err := page.Eval(autoFillJS,
		cfg.LoginConf.Username, cfg.LoginConf.Password,
		cfg.LoginConf.Email, cfg.LoginConf.Phone); err != nil {
		log.Logger.Debugf("auto fill err: %s", err)
	}

	// 2. 记录基线链接
	collect()

	// 3. 记录基线链接
	collect()

	// 4. 逐个点击，每次点完重新扫描
	//
	// 每个元素最多点 maxRepeat 轮：多步流程（向导/解锁/结算）
	// 需要反复点同一个按钮才会逐步放出后续链接，只点一次拿不到。
	maxRepeat := cfg.AutoConf.MaxClickRepeat
	if maxRepeat <= 0 {
		maxRepeat = 3
	}
	res, err := page.Eval(listClickableJS, cfg.AutoConf.Filter)
	if err != nil {
		log.Logger.Debugf("Auto list clickable err: %s", err)
		return static.HandlerUrls(hrefList, startURL)
	}
	items := res.Value.Arr()
	log.Logger.Debugf("auto clickable count: %d", len(items))

	for i := range items {
		if time.Now().After(deadline) {
			log.Logger.Debugf("auto budget exhausted at %d/%d", i, len(items))
			break
		}
		if skip, ok := items[i].Map()["skip"]; ok && skip.Bool() {
			continue
		}
		noProgress := 0
		sig := ""
		if s, ok := items[i].Map()["sig"]; ok {
			sig = s.Str()
		}
		for round := 0; round < maxRepeat; round++ {
			if time.Now().After(deadline) {
				break
			}
			_, _ = page.Eval(armMutationJS)
			if _, err := page.Eval(clickBySigJS, sig); err != nil {
				// 上下文失效 = 点击把页面导航走了，回起点继续下一个
				log.Logger.Debugf("auto click %s navigated away: %s", sig, err)
				backToStart(page, startURL)
				break
			}
			changed := settleAfterClick(page, maxSettle)
			before := len(hrefList)
			collect()
			if navigatedAway(currentURL(page), startURL) {
				log.Logger.Debugf("auto navigated to %s, back to start", currentURL(page))
				backToStart(page, startURL)
				break
			}
			// 多步流程（解锁/面包屑/问卷/结算）必须点满次数，不能提前退出。
			//
			// 实测 unlock 按钮：第 1 次只更新计数器、不出链接，
			// 第 2 次才出链接。若因「本轮无变化」就 break，这条链就断了
			// （multi-step 会从 14 掉到 4）。
			//
			// 但也不能无限制重复：首页有个元素会持续触发 DOM 变化，
			// 实测被点了 16 轮，把预算吃光，后面的元素全轮不到。
			// 所以用「连续无进展」计数来刹车：只要还在出链接就继续。
			if len(hrefList) > before {
				noProgress = 0
			} else {
				noProgress++
				if noProgress >= 2 && !changed {
					break
				}
			}
		}
	}

	// 4b. 处理点击后「新生成」的可点击元素。
	//
	// 有些入口是两步的：先点一个按钮，页面才生成真正的入口按钮
	// （如「确认后展开」生成的确认按钮）。上面那份列表在开头就固定了，
	// 看不到后来才出现的元素，这里再扫一轮把它们补上。
	//
	// 用「签名集合差」找新元素，而不是拿下标切片：
	// 新按钮不一定插在列表末尾，按下标取会取到旧元素。
	if time.Now().Before(deadline) {
		if res, err := page.Eval(listClickableJS, cfg.AutoConf.Filter); err == nil {
			more := res.Value.Arr()
			oldSigs := map[string]bool{}
			for i := range items {
				if s, ok := items[i].Map()["sig"]; ok {
					oldSigs[s.Str()] = true
				}
			}
			for i := range more {
				if time.Now().After(deadline) {
					break
				}
				m := more[i].Map()
				if skip, ok := m["skip"]; ok && skip.Bool() {
					continue
				}
				sig := ""
				if s, ok := m["sig"]; ok {
					sig = s.Str()
				}
				if sig == "" || oldSigs[sig] {
					continue
				}
				log.Logger.Debugf("auto extra element: %s", sig)
				_, _ = page.Eval(armMutationJS)
				if _, err := page.Eval(clickBySigJS, sig); err != nil {
					backToStart(page, startURL)
					continue
				}
				settleAfterClick(page, maxSettle)
				collect()
				oldSigs[sig] = true
			}
		}
	}


	// 5. 派发非 click 事件（右键/双击/select/快捷键）。
	//
	// 这些入口大多触发 XHR 而不是 DOM 链接，但请求会被流量劫持捕获；
	// select 的 value 里则可能直接写着目标地址，单独收回来。
	if time.Now().Before(deadline) {
		if res, err := page.Eval(dispatchEventsJS, cfg.AutoConf.ShortcutKey); err == nil {
			for _, v := range res.Value.Arr() {
				h := v.Str()
				if h != "" && !seen[h] {
					seen[h] = true
					hrefList = append(hrefList, h)
				}
			}
		}
		maxSettle = 1200 * time.Millisecond
		settleAfterClick(page, maxSettle)
		collect()
		if navigatedAway(currentURL(page), startURL) {
			backToStart(page, startURL)
		}
	}

	log.Logger.Debugf("auto collected %d links", len(hrefList))
	return static.HandlerUrls(hrefList, startURL)
}
