package inject

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/static"
	"argo/pkg/utils"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// 可点击元素的选择器。
//
// 除了 <button> / <input>，还覆盖 select、带 onclick 的元素、SVG 热点，
// 以及用 ARIA role 标成按钮/标签页/菜单项的容器——SPA 里大量入口是这些。
const autoClickableSelector = "a[href^='javascript'], button, input[type=button], input[type=submit], select, [onclick], svg, [role=button], [role=tab], [role=menuitem], summary"

// formFieldMatcherSrc 是表单字段匹配的公共 JS 片段（函数定义），拼进各填充函数体内。
//
// 填充优先级：skip 规则 > 用户 rules（正则对 name/id/placeholder/aria-label）> 内置语义 > 调用方默认值。
// 验证码类字段默认 skip——爬虫不该猜验证码，填假值反而会把表单卡在校验失败。
const formFieldMatcherSrc = `
	function fieldKey(node) {
		const parts = [node.name, node.id, node.getAttribute && node.getAttribute("placeholder"),
			node.getAttribute && node.getAttribute("aria-label")];
		return parts.filter(Boolean).join(" ").toLowerCase();
	}
	function regexTest(pattern, s) {
		try { return new RegExp(pattern).test(s); } catch (e) { return s.indexOf(pattern) >= 0; }
	}
	function formSkipped(node, skipRes) {
		const k = fieldKey(node);
		return skipRes.some(function (r) { return regexTest(r, k); });
	}
	function formRuleValue(node, rules) {
		const k = fieldKey(node);
		for (let i = 0; i < rules.length; i++) {
			if (regexTest(rules[i].match, k)) { return rules[i].value; }
		}
		return null;
	}
	function semanticValue(node, defaults) {
		const t = (node.type || "text").toLowerCase();
		const k = fieldKey(node);
		if (t === "search" || /search|搜索|查询/.test(k)) { return defaults.search; }
		if (t === "email" || /mail|邮箱/.test(k)) { return defaults.email; }
		if (t === "tel" || /phone|手机|电话/.test(k)) { return defaults.phone; }
		if (t === "url") { return "https://example.com"; }
		if (t === "number" || t === "range") { return "20"; }
		if (t === "date") { return "2020-01-01"; }
		if (t === "password") { return defaults.password; }
		if (t === "text" || t === "") { return defaults.username; }
		return null;
	}
`

// autoFillJS 给页面上的输入框按字段级规则填充。
//
// 必须走原生 value setter 并派发 input/change：
// 直接写 textContent/nodeValue 对 input 无效，
// 而 React/Vue 这类框架靠覆写 value setter 感知输入。
var autoFillJS = `(username, password, email, phone, rulesJson) => {` + formFieldMatcherSrc + `
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
	let conf = {};
	try { conf = JSON.parse(rulesJson || "{}") || {}; } catch (e) {}
	const userRules = conf.rules || [];
	const skipRes = conf.skip || [];
	const defaults = {username: username, password: password, email: email, phone: phone, search: "argo"};
	document.querySelectorAll("input, textarea").forEach(function (node) {
		const t = (node.type || "text").toLowerCase();
		if (t === "checkbox" || t === "radio") {
			// checkbox/radio 必须在交互点击前勾上：
			// 「先勾选再点提交类按钮」的流程，按钮点击时读的是 checked 状态
			if (!node.checked) {
				node.checked = true;
				node.dispatchEvent(new Event("input", {bubbles: true}));
				node.dispatchEvent(new Event("change", {bubbles: true}));
			}
			return;
		}
		if (node.value && node.value.length > 0) { return; }
		if (formSkipped(node, skipRes)) { return; }
		const value = formRuleValue(node, userRules) || semanticValue(node, defaults);
		if (value !== null) { setValue(node, value); }
	});
	return true;
}`

// scanLinksJS 扫描当前 DOM 里所有可访问链接，返回绝对地址。
// 用浏览器解析后的 a.href，避免页面跳转后相对路径算错。
var scanLinksJS = `() => {
	const out = [];
	const seen = new Set();
	function add(v) {
		if (!v) { return; }
		const s = String(v).trim();
		if (!s || s === "#" || s.indexOf("javascript:") === 0 || s.indexOf("mailto:") === 0 || s.indexOf("tel:") === 0) { return; }
		try {
			const abs = new URL(s, document.baseURI).href;
			if (abs.indexOf("http") === 0 && !seen.has(abs)) { seen.add(abs); out.push(abs); }
		} catch (e) {}
	}
	document.querySelectorAll("a[href], area[href], iframe[src], frame[src], form[action]").forEach(function (el) {
		add(el.getAttribute("href") || el.getAttribute("src") || el.getAttribute("action"));
	});
	// javascript: 协议静态提取：href/src 里的跳转代码（如 javascript:location='/x'）
	// 里的字符串字面量——不执行也能拿到 URL（crawlergo TriggerJavascriptProtocol 同思路）
	document.querySelectorAll('[href^="javascript:" i], [src^="javascript:" i]').forEach(function (el) {
		const code = el.getAttribute("href") || el.getAttribute("src") || "";
		const re = /['"]([^'"]{2,300})['"]/g;
		let m;
		while ((m = re.exec(code)) !== null) {
			const v = m[1];
			if (v.indexOf("/") >= 0 || /^https?:/i.test(v)) { add(v); }
		}
	});
	return out;
}`

// routesCollectJS 收集 listener 钩子在源头记录的 SPA 路由变化
// （pushState/replaceState/hashchange/popstate，不一定出现在 DOM 里）。
var routesCollectJS = `() => {
	const out = [];
	(window.__argoRoutes || []).forEach(function (u) {
		try {
			const abs = new URL(String(u), document.baseURI).href;
			if (abs.indexOf("http") === 0) { out.push(abs); }
		} catch (e) {}
	});
	return out;
}`

var scanLinksDeepJS = `() => {
	const out = [];
	const seen = new Set();
	function add(v) {
		if (!v) { return; }
		const s = String(v).trim();
		if (!s || s === "#" || s.indexOf("javascript:") === 0 || s.indexOf("mailto:") === 0 || s.indexOf("tel:") === 0) { return; }
		try {
			const abs = new URL(s, document.baseURI).href;
			if (abs.indexOf("http") === 0 && !seen.has(abs)) { seen.add(abs); out.push(abs); }
		} catch (e) {}
	}
	function looksLikeURL(v) {
		const s = String(v).trim();
		if (s.length < 4 || s.length > 2048) { return false; }
		if (s.indexOf("/") === 0) { return true; }
		if (/^https?:\/\//i.test(s)) { return true; }
		if (s.indexOf("/") > 0 && !/\s/.test(s)) { return true; }
		return false;
	}
	function scanAttrs(root) {
		let all;
		try { all = root.querySelectorAll("*"); } catch (e) { return; }
		Array.prototype.forEach.call(all, function (el) {
			const tag = el.tagName;
			if (tag === "SCRIPT" || tag === "STYLE" || tag === "NOSCRIPT") { return; }
			const n = el.attributes ? el.attributes.length : 0;
			for (let i = 0; i < n; i++) {
				const a = el.attributes[i];
				// class/style/id/value 是高噪声属性，跳过减少误报
				if (a.name === "class" || a.name === "style" || a.name === "id" || a.name === "value") { continue; }
				const v = a.value;
				if (v && looksLikeURL(v)) { add(v); }
			}
		});
	}
	scanAttrs(document);
	const walked = new Set();
	(function walk(root, depth) {
		if (depth > 6) { return; }
		let hosts;
		try { hosts = root.querySelectorAll("*"); } catch (e) { return; }
		Array.prototype.forEach.call(hosts, function (el) {
			if (!el || walked.has(el)) { return; }
			let sr = null;
			try { sr = el.shadowRoot; } catch (e) { return; }
			if (!sr) { return; }
			walked.add(el);
			scanAttrs(sr);
			walk(sr, depth + 1);
		});
	})(document, 0);
	return out;
}`

var submitFormsJS = `async (username, password, email, phone, rulesJson) => {` + formFieldMatcherSrc + `
	const done = [];
	let conf = {};
	try { conf = JSON.parse(rulesJson || "{}") || {}; } catch (e) {}
	const userRules = conf.rules || [];
	const skipRes = conf.skip || [];
	const defaults = {username: username, password: password, email: email, phone: phone, search: "argo"};
	function fill(el) {
		if (el.value && el.value.length > 0) { return; }
		if (el.tagName !== "SELECT" && formSkipped(el, skipRes)) { return; }
		const ruleVal = formRuleValue(el, userRules);
		if (ruleVal !== null) { el.value = ruleVal; }
		else {
			const v = semanticValue(el, defaults);
			if (v === null) { return; }
			el.value = v;
		}
		try {
			el.dispatchEvent(new Event("input", {bubbles: true}));
			el.dispatchEvent(new Event("change", {bubbles: true}));
		} catch (e) {}
	}
	Array.prototype.forEach.call(document.querySelectorAll("form"), function (f) {
		const action = f.getAttribute("action") || "";
		if (!action) { return; }
		// 跳过登出类表单，避免把会话搞没
		const low = (action + " " + (f.innerHTML || "")).toLowerCase();
		if (low.indexOf("logout") >= 0 || low.indexOf("signout") >= 0) { return; }
		Array.prototype.forEach.call(f.querySelectorAll("input, select, textarea"), function (el) {
			if (!el.name) { return; }
			if (el.tagName === "SELECT") {
				if (!el.value) {
					for (let i = 0; i < el.options.length; i++) {
						if (el.options[i].value) { el.selectedIndex = i; break; }
					}
				}
				return;
			}
			fill(el);
		});
		// 蜘蛛走位到表单中心再提交（有头模式）
		if (window.__argoSpider) {
			try {
				const r = f.getBoundingClientRect();
				const act = f.getAttribute("action") || "";
				window.__argoSpider.tap(r.left + r.width / 2, r.top + r.height / 2, r.width, r.height, "FORM " + act.slice(0, 20));
			} catch (e) {}
		}
		// 优先用 requestSubmit（会跑校验并触发 submit 事件），退回 submit()
		try {
			if (typeof f.requestSubmit === "function") { f.requestSubmit(); }
			else { f.submit(); }
			done.push(action);
		} catch (e) {
			try { f.submit(); done.push(action); } catch (e2) {}
		}
	});
	return done;
}`

// formRulesJSON 把配置里的填充规则序列化成 JS 侧参数，并校验正则合法性。
// 正则非法返回错误，由调用方在启动时 Fatal——用户第一时间知道哪条规则写错。
func formRulesJSON(cfg conf.FormConf) (string, error) {
	payload := struct {
		Rules []conf.FormFillRule `json:"rules"`
		Skip  []string            `json:"skip"`
	}{Rules: cfg.Rules, Skip: cfg.Skip}
	for i, rule := range payload.Rules {
		if _, err := regexp.Compile(rule.Match); err != nil {
			return "", fmt.Errorf("form.rules[%d] match %q: %s", i, rule.Match, err)
		}
	}
	for i, pattern := range payload.Skip {
		if _, err := regexp.Compile(pattern); err != nil {
			return "", fmt.Errorf("form.skip[%d] %q: %s", i, pattern, err)
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// clickableCollectorSrc 可点击元素收集器（拼进 listClickableJS / clickBySigJS 函数体）。
//
// 三路来源，按通用信号收敛：
//  1. 结构选择器（button/[onclick]/[role=*] 等，调用方传入）
//  2. __argoLsn 标记 —— listener_hook 在 document 起点劫持 addEventListener，
//     框架（Vue/React）用 addEventListener 绑 click 的 div/span 全在这里被捕获
//     （SPA 实测大量入口是 <div @click>，无任何结构特征，选择器永远捞不到）
//  3. cursor:pointer 计算样式兜底；大 DOM 下计算样式昂贵，超过扫描上限即放弃这一路
const clickableCollectorSrc = `
	function argoFrames() {
		// 同源 iframe 的 document 列表（跨源访问 contentDocument 会抛错，跳过）
		const frames = [document];
		try {
			document.querySelectorAll("iframe").forEach(function (f, i) {
				try { if (f.contentDocument) { frames.push(f.contentDocument); } } catch (e) {}
			});
		} catch (e) {}
		return frames;
	}
	function argoClickableNodes(selector) {
		// 同源 iframe 内的元素同样收集（真实站点 iframe 菜单/表单入口），
		// sig 前缀 f<idx>| 标记所在 frame 供点击时定位
		let out = [];
		let cursorExtra = 0;
		argoFrames().forEach(function (doc, fIdx) {
			const base = fIdx === 0 ? out : [];
			const nodes = Array.prototype.slice.call(doc.querySelectorAll(selector));
			if (fIdx === 0) {
				out = nodes;
			} else {
				nodes.forEach(function (n) { n.__argoFrame = fIdx; out.push(n); });
			}
		});
		try {
			const all = document.querySelectorAll("*");
			for (let i = 0; i < all.length; i++) {
				const el = all[i];
				if (el.__argoLsn && el.__argoLsn.indexOf("click") >= 0 && el.__argoFrame === undefined) {
					out.push(el);
					continue;
				}
				if (i > 1500 || cursorExtra >= 30) { break; } // 噪声封顶：死元素点一轮 ~0.5s，多了烧穿预算
				const tag = el.tagName;
				if (tag === "SCRIPT" || tag === "STYLE" || tag === "NOSCRIPT" || tag === "A" || tag === "BUTTON") { continue; }
				if (!el.offsetParent && tag !== "BODY") { continue; }
				try {
					if (window.getComputedStyle(el).cursor === "pointer") { out.push(el); cursorExtra++; }
				} catch (e) {}
			}
		} catch (e) {}
		return out.filter(function (v, i, a) { return a.indexOf(v) === i; });
	}
`

// listClickableJS 列出可点击元素，附带稳定签名与是否应跳过。
//
// 签名用于跨轮次识别「这个元素已经点过了」：
// 每轮重新列举时列表里会包含旧按钮，不排重的话预算全浪费在重复点击上。
var listClickableJS = fmt.Sprintf(`(filters) => {`+clickableCollectorSrc+`
	const nodes = argoClickableNodes(%s);
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
		const prefix = el.__argoFrame ? ("f" + el.__argoFrame + "|") : "";
		return { index: i, skip: skip, tag: el.tagName, id: el.id || "",
		         sig: prefix + base + "|" + counter[base] };
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
var clickBySigJS = fmt.Sprintf(`async (targetSig) => {`+clickableCollectorSrc+`
	const nodes = argoClickableNodes(%s);
	const counter = {};
	let el = null;
	for (let i = 0; i < nodes.length; i++) {
		let text = "";
		try { text = (nodes[i].textContent || "").replace(/[0-9]+/g, "#").trim().slice(0, 24); } catch (e) {}
		const base = nodes[i].tagName + "#" + (nodes[i].id || "") + "|" + text;
		counter[base] = (counter[base] || 0) + 1;
		const prefix = nodes[i].__argoFrame ? ("f" + nodes[i].__argoFrame + "|") : "";
		if (prefix + base + "|" + counter[base] === targetSig) { el = nodes[i]; break; }
	}
	if (!el) { return false; }
	// 蜘蛛动画（有头模式注入时）：爬过去伸腿点中，再执行真实点击；
	// 无头未注入时 __argoSpider 不存在，直接跳过零开销
	// 先滚进视口再取坐标：元素在视口外时，先 tap 后滚动会点在错误位置
	try { el.scrollIntoView({ block: "center" }); } catch (e) {}
	// 蜘蛛动画（有头模式注入时）：fire-and-forget——点击立即执行，蜘蛛异步追过去
	// 表演。动画绝不阻塞交互（实测 await 版本把每页交互拖慢 ~1s/次）；
	// 无头未注入时 __argoSpider 不存在，直接跳过零开销
	if (window.__argoSpider) {
		try {
			const rect = el.getBoundingClientRect();
			const label = el.tagName + (el.id ? "#" + el.id : "") +
				(!el.id && el.textContent ? " \"" + el.textContent.trim().slice(0, 10) + "\"" : "");
			window.__argoSpider.tap(rect.left + rect.width / 2, rect.top + rect.height / 2, rect.width, rect.height, label);
		} catch (e) {}
	}
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
	// click() 是 HTMLElement 的方法，SVG / 自定义元素上不存在
	//（rod 探针实测 svg.click 抛 "is not a function"），统一兜底派发 MouseEvent
	try {
		if (typeof el.click === "function") { el.click(); }
		else { el.dispatchEvent(new MouseEvent("click", {bubbles: true, cancelable: true, view: window})); }
	} catch (e) { return false; }
	return true;
}`, fmt.Sprintf("%q", autoClickableSelector))

// dispatchEventsJS 向页面派发非 click 的交互事件。
//
// 很多入口挂在 contextmenu / dblclick / change / keydown 上，
// 只发 click 永远触发不到它们。
//
// 关键点：每派发一个事件就立即收集一次文档里的 a[href] 增量。
// 站点惯用法是「change/右键时清空容器再放新链接」——
// 全部派发完再收集的话，容器里只剩最后一个事件产生的链接，
// 之前的全部丢失（实测 3 个 select option 只能收到最后 1 个）。
//
// select 派发前有蜘蛛动画联动（async；蜘蛛未注入时直接跳过）。
var dispatchEventsJS = `async (shortcutKey) => {
	const found = [];
	const seen = new Set();
	function safe(fn) { try { fn(); } catch (e) {} }
	function push(v) {
		if (!v) { return; }
		const s = String(v).trim();
		if (!s || seen.has(s)) { return; }
		try {
			const abs = new URL(s, document.baseURI).href;
			if (abs.indexOf("http") === 0 && !seen.has(abs)) { seen.add(abs); found.push(abs); }
		} catch (e) {}
	}
	// 收集当前文档全部链接（相对路径解析为绝对地址）
	function harvest() {
		document.querySelectorAll("a[href]").forEach(function (a) { push(a.getAttribute("href")); });
	}

	// 右键菜单：常见于弹出隐藏操作入口。
	// 蜘蛛只对真正挂了 oncontextmenu 的元素走位点击（宽泛猜测的元素太多，逐个走位太慢）
	const ctxEls = Array.prototype.slice.call(document.querySelectorAll("[oncontextmenu]"));
	for (const el of ctxEls) {
		if (window.__argoSpider) {
			try { const r = el.getBoundingClientRect(); window.__argoSpider.tap(r.left + r.width / 2, r.top + r.height / 2, r.width, r.height, el.tagName + " ctx/dbl"); } catch (e) {}
		}
		safe(function () { el.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, view: window })); });
		harvest();
	}
	safe(function () {
		document.querySelectorAll("body, [id*=ctx], [class*=ctx], [class*=context], [id*=menu], [class*=menu]").forEach(function (el) {
			safe(function () {
				el.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, view: window }));
			});
			harvest();
		});
	});

	// 双击：挂了 ondblclick 的元素逐个走位点双击，其余批量派发
	const dblEls = Array.prototype.slice.call(document.querySelectorAll("[ondblclick]"));
	for (const el of dblEls) {
		if (window.__argoSpider) {
			try { const r = el.getBoundingClientRect(); window.__argoSpider.tap(r.left + r.width / 2, r.top + r.height / 2, r.width, r.height, el.tagName + " ctx/dbl"); } catch (e) {}
		}
		safe(function () { el.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, view: window })); });
		harvest();
	}
	safe(function () {
		document.querySelectorAll("button, [role=button]").forEach(function (el) {
			safe(function () {
				el.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, view: window }));
			});
			harvest();
		});
	});

	// 下拉框：选中每一项、派发 change 后立即收集——
	// handler 常在 change 里清空容器再放新链接，逐项收集才能全拿到
	const sels = Array.prototype.slice.call(document.querySelectorAll("select"));
	for (const sel of sels) {
		for (let i = 0; i < sel.options.length; i++) {
			if (window.__argoSpider) {
				try {
					const r = sel.getBoundingClientRect();
					const opt = sel.options[i];
					const label = "SELECT" + (sel.id ? "#" + sel.id : "") + " \u2192 " + ((opt && (opt.textContent || opt.value)) || "").trim().slice(0, 12);
					window.__argoSpider.tap(r.left + r.width / 2, r.top + r.height / 2, r.width, r.height, label);
				} catch (e) {}
			}
			safe(function () {
				sel.selectedIndex = i;
				sel.dispatchEvent(new Event("input", { bubbles: true }));
				sel.dispatchEvent(new Event("change", { bubbles: true }));
			});
			harvest();
			push(sel.value);
			push(sel.options[i] && sel.options[i].value);
		}
	}

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
		harvest();
	});

	return found;
}`

// hoverCandidatesJS 列出值得真实鼠标悬停的元素中心坐标。
//
// CSS :hover 展开的菜单用 JS dispatchEvent 触发不了（伪类只有真实输入才生效），
// 必须走 CDP Input 鼠标移动。候选：绑了 mouseover/mouseenter 的元素（__argoLsn）
// 加 nav/menu 类容器，封顶 20 个控制成本。
var hoverCandidatesJS = `() => {
	const out = [];
	const seen = new Set();
	function push(el) {
		if (out.length >= 20 || seen.has(el)) { return; }
		try {
			const r = el.getBoundingClientRect();
			if (r.width < 2 || r.height < 2) { return; }
			if (r.left < 0 || r.top < 0 || r.right > window.innerWidth || r.bottom > window.innerHeight) { return; }
			seen.add(el);
			out.push({x: r.left + r.width / 2, y: r.top + r.height / 2,
				tag: el.tagName + (el.id ? "#" + el.id : "")});
		} catch (e) {}
	}
	document.querySelectorAll("nav, [class*=menu], [class*=nav], [class*=dropdown], [class*=drop-down]").forEach(push);
	document.querySelectorAll("*").forEach(function (el) {
		if (el.__argoLsn && (el.__argoLsn.indexOf("mouseover") >= 0 || el.__argoLsn.indexOf("mouseenter") >= 0)) { push(el); }
	});
	return out;
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

// interactionBudgetCap 单页交互预算上限。
//
// 预算公式里的 browserTimeout/4 项在默认 browserTimeout=900 下高达 225s，
// 会让挂起页面把外层强杀拖到 235s（历史行为是 tabTimeout=15s 就杀），
// 默认口径下整个任务被单个坏页面拖死。交互超过 60s 还没有产出，
// 继续等的收益趋近于零，直接封顶。
const interactionBudgetCap = 60 * time.Second

// InteractionBudget 返回单页交互的时间预算。
//
// 外层 tab 的强杀超时必须与这里用同一个值（见 tab.go NewTab）：
// 历史上外层只等 tabTimeout（15s），而交互内部预算是 max(tabTimeout, browserTimeout/4)（35s），
// 两个时钟不一致导致交互跑一半页面就被杀——元素多的首页后半部分按钮
// （购物车/确认门/多级菜单）全部没轮到，却表现为「交互能力缺失」。
func InteractionBudget() time.Duration {
	budget := time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second
	globalShare := time.Duration(conf.GlobalConfig.BrowserConf.BrowserTimeout) * time.Second / 4
	if globalShare > budget {
		budget = globalShare
	}
	if budget < 12*time.Second {
		budget = 12 * time.Second
	}
	if budget > interactionBudgetCap {
		budget = interactionBudgetCap
	}
	return budget
}

// Auto 在页面上做自动化交互，返回发现的 URL。
//
// reportProgress 可为 nil；不为 nil 时在页面产生进展（新链接/DOM 变化）时被调用，
// 供外层做「进度驱动」的 idle 超时判断——有进展的页面不该被固定时钟杀掉
// （katana 的 heuristic 策略同理：等静默信号而不是固定寿命）。
//
// 采用「Go 驱动」而不是把整套流程写进一个 JS 函数：
// 点击可能触发页面导航，一旦导航发生，浏览器里的 JS 执行上下文立刻失效，
// 把所有点击和等待塞在一个 JS 里必然中途崩掉（历史实现正是如此，
// 且收集完静态链接就 return，点击新产生的链接从未被读取）。
// 所以这里 JS 只做单步动作，循环、重扫、导航恢复都由 Go 侧控制。
func Auto(page *rod.Page, reportProgress func()) []string {
	ping := func() {
		if reportProgress != nil {
			reportProgress()
		}
	}
	info, err := utils.GetPageInfoByPage(page)
	if err != nil {
		return nil
	}
	startURL := info.URL
	log.Logger.Debugf("run auto js %s", startURL)

	cfg := conf.GlobalConfig
	// 规则正则在 engine 启动时已校验过；这里再失败只降级为空规则并记日志，不中断爬取
	formRules, err := formRulesJSON(cfg.FormConf)
	if err != nil {
		log.Logger.Errorf("form fill rules invalid, fallback to defaults: %s", err)
		formRules = "{}"
	}
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
	// 交互预算 = max(tabTimeout, browserTimeout/4)。
	//
	// 单页交互如果只按 tabTimeout 算，元素多的页面跑不完
	// （实测小预算时 multi-step 一条都拿不到）；
	// 但也不能拿满全局预算：tab 是并发的，单页吃满会挤掉同批其他页面，
	// 且任务总时长会撞上 browserTimeout 被硬切
	// （实测产生 200 条与 80 条的双峰波动，shop/pagination 大量丢失）。
	// 所以给一个居中的上限：全局预算的四分之一。
	budget := InteractionBudget()
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
			ping()
		}
		// SPA 路由源头捕获（listener 钩子记录的路由变化）
		if res, err := page.Eval(routesCollectJS); err == nil {
			for _, v := range res.Value.Arr() {
				h := v.Str()
				if h == "" || seen[h] {
					continue
				}
				seen[h] = true
				hrefList = append(hrefList, h)
				ping()
			}
		}
	}

	collectDeep := func() {
		res, err := page.Eval(scanLinksDeepJS)
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
			ping()
		}
	}

	// 1. 先填表单，让需要登录的页面进入登录态
	if _, err := page.Eval(autoFillJS,
		cfg.LoginConf.Username, cfg.LoginConf.Password,
		cfg.LoginConf.Email, cfg.LoginConf.Phone, formRules); err != nil {
		log.Logger.Debugf("auto fill err: %s", err)
	}

	// 2. 记录基线链接（含冷路径全量扫描）
	collect()
	collectDeep()

	// 3. 记录基线链接
	collect()

	// 3b. 先派发非 click 事件（右键/双击/select 遍历/快捷键）。
	//
	// 放在点击循环之前而不是收尾：这些派发很便宜（几次 dispatch + 收集），
	// 而点击循环可能耗尽预算——放在最后的话，预算一紧整组事件全部跳过
	// （实测 tabtimeout 紧的 run 里 ctx/dblclick/select/shortcut 全缺）。
	// select 的 value 与派发产生的新链接在这里一并收集。
	if res, err := page.Eval(dispatchEventsJS, cfg.AutoConf.ShortcutKey); err == nil {
		for _, v := range res.Value.Arr() {
			h := v.Str()
			if h != "" && !seen[h] {
				seen[h] = true
				hrefList = append(hrefList, h)
				ping()
			}
		}
	}
	settleAfterClick(page, 1200*time.Millisecond)
	collect()
	if navigatedAway(currentURL(page), startURL) {
		backToStart(page, startURL)
	}

	// 4. 逐个点击，每次点完重新扫描
	//
	// 每个元素最多点 maxRepeat 轮：多步流程（向导/解锁/结算）
	// 需要反复点同一个按钮才会逐步放出后续链接，只点一次拿不到。
	// 默认 5：多步流程（向导 4 步、结算 4 步、解锁 3 步）
	// 实测默认 3 时最后一步拿不到，改 5 后 multi-step 明显提升。
	maxRepeat := cfg.AutoConf.MaxClickRepeat
	if maxRepeat <= 0 {
		maxRepeat = 5
	}
	res, err := page.Eval(listClickableJS, cfg.AutoConf.Filter)
	if err != nil {
		log.Logger.Debugf("Auto list clickable err: %s", err)
		return static.HandlerUrls(hrefList, startURL)
	}
	items := res.Value.Arr()
	log.Logger.Debugf("auto clickable count: %d", len(items))

	// 两遍制（广度优先，再择深）：
	//
	// 单循环深度优先的实测问题：页面上 10 个 tab，第一个 tab 每次点击都触发
	// DOM 变化（changed=true 永不提前退），maxRepeat 轮点完才轮到第二个，
	// 预算烧光后面 9 个 tab 从未被点击。
	//
	// 第一遍：每个元素保证点一次——所有 tab/菜单挨个轮到；
	// 第二遍：只对第一遍「有反应」的元素重复点击（多步流程在这里推进）。
	firstReacted := make(map[string]bool, len(items))
	clickSig := func(sig string, settle time.Duration) (changed bool, gained int, navAway bool) {
		_, _ = page.Eval(armMutationJS)
		if _, err := page.Eval(clickBySigJS, sig); err != nil {
			// 上下文失效 = 点击把页面导航走了，回起点继续下一个
			log.Logger.Debugf("auto click %s navigated away: %s", sig, err)
			backToStart(page, startURL)
			return false, 0, true
		}
		changed = settleAfterClick(page, settle)
		before := len(hrefList)
		collect()
		if navigatedAway(currentURL(page), startURL) {
			backToStart(page, startURL)
			return changed, len(hrefList) - before, true
		}
		return changed, len(hrefList) - before, false
	}

	// 第一遍：广度——每个元素一次
	for i := range items {
		if time.Now().After(deadline) {
			log.Logger.Debugf("auto budget exhausted at %d/%d (pass1)", i, len(items))
			break
		}
		m := items[i].Map()
		if skip, ok := m["skip"]; ok && skip.Bool() {
			continue
		}
		sig := ""
		if v, ok := m["sig"]; ok {
			sig = v.Str()
		}
		if sig == "" {
			continue
		}
		changed, gained, _ := clickSig(sig, maxSettle)
		firstReacted[sig] = changed || gained > 0
	}

	// 第二遍：择深——只重复有反应的元素；剩余预算不足 30% 时不再开新链
	for i := range items {
		if time.Now().After(deadline) {
			log.Logger.Debugf("auto budget exhausted at %d/%d (pass2)", i, len(items))
			break
		}
		m := items[i].Map()
		if skip, ok := m["skip"]; ok && skip.Bool() {
			continue
		}
		sig := ""
		if v, ok := m["sig"]; ok {
			sig = v.Str()
		}
		if sig == "" || !firstReacted[sig] {
			continue
		}
		noProgress := 0
		for round := 1; round < maxRepeat; round++ {
			if time.Now().After(deadline) {
				break
			}
			// 预算保护：剩余不足 30% 且已推进 2 轮，让位给其它页面
			if time.Until(deadline) < budget*3/10 && round > 2 {
				break
			}
			_, gained, _ := clickSig(sig, maxSettle)
			// 多步流程（解锁/面包屑/问卷/结算）要反复点同一按钮才逐步出链接；
			// 连续两轮无新链即收（unlock 第 1 轮只更新计数器、第 2 轮出链接，
			// 所以阈值是 2 不是 1）
			if gained > 0 {
				noProgress = 0
			} else {
				noProgress++
				if noProgress >= 2 {
					break
				}
			}
		}
	}

	// 真实鼠标 hover：CSS :hover 展开的菜单（下拉导航等）JS 事件触发不了，
	// 用 CDP Input 真鼠标逐个悬停（封顶 20 个），有 DOM 变化就收集。
	// 放在两遍点击之后：hover 是增强项，不能挤占广度/深度的预算
	//（实测插在中间会吃掉 8-10s，无 hover 菜单的站点纯亏）
	if time.Until(deadline) > budget*15/100 {
		if res, err := page.Eval(hoverCandidatesJS); err == nil {
			mouse := page.Mouse
			for _, v := range res.Value.Arr() {
				if time.Now().After(deadline) {
					break
				}
				m := v.Map()
				x, y := m["x"].Num(), m["y"].Num()
				if x <= 0 || y <= 0 {
					continue
				}
				_, _ = page.Eval(armMutationJS)
				if err := mouse.MoveTo(proto.Point{X: x, Y: y}); err != nil {
					continue
				}
				settleAfterClick(page, 350*time.Millisecond)
				collect()
				if navigatedAway(currentURL(page), startURL) {
					backToStart(page, startURL)
				}
			}
			_ = mouse
		}
	}

	// 4b. 迭代处理点击后「新生成」的可点击元素。
	//
	// 有些入口是多层的：点一个按钮，页面才生成下一个入口按钮，
	// 点它又生成再下一层（实测菜单链三层、确认门两层）。
	// 只补扫一轮的话，第三层永远拿不到。
	//
	// 收敛循环：反复用「签名集合差」找没点过的新元素并点掉，
	// 直到没有新元素或预算耗尽。通用机制——不认识具体页面，只按「新元素」驱动。
	if time.Now().Before(deadline) {
		clickedSigs := map[string]bool{}
		for i := range items {
			if s, ok := items[i].Map()["sig"]; ok {
				clickedSigs[s.Str()] = true
			}
		}
		for pass := 0; pass < 4; pass++ { // 深度上限：防止页面无限生成元素
			if time.Now().After(deadline) {
				break
			}
			res, err := page.Eval(listClickableJS, cfg.AutoConf.Filter)
			if err != nil {
				// 页面可能被导航走了，回起点重新扫
				backToStart(page, startURL)
				continue
			}
			more := res.Value.Arr()
			fresh := 0
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
				if sig == "" || clickedSigs[sig] {
					continue
				}
				clickedSigs[sig] = true
				fresh++
				log.Logger.Debugf("auto extra element: %s", sig)
				_, _ = page.Eval(armMutationJS)
				if _, err := page.Eval(clickBySigJS, sig); err != nil {
					backToStart(page, startURL)
					continue
				}
				collect()
				changed := settleAfterClick(page, maxSettle)
				collect()
				// 这个新元素还有反应就多给几轮（它可能是又一层菜单入口）
				if changed {
					for r := 1; r < maxRepeat; r++ {
						if time.Now().After(deadline) {
							break
						}
						_, _ = page.Eval(armMutationJS)
						if _, err := page.Eval(clickBySigJS, sig); err != nil {
							break
						}
						before := len(hrefList)
						collect()
						if len(hrefList) == before {
							break
						}
					}
				}
			}
			// 本轮没有新元素可点，说明已收敛
			if fresh == 0 {
				break
			}
		}
	}
	// 5. 表单提交。
	//
	// 只读 <form action> 拿不到真实方法与参数；
	// 表单类入口（反馈/订阅/上传/严格校验）要求的就是 POST 请求，
	// 必须真正提交，请求才会被流量劫持捕获。
	// 放在交互之后：提交有副作用（跳转），不能打断前面的点击循环。
	// 等待压到 600ms：提交只是为了让 POST 请求发出去，流量劫持会立即捕获；
	// 等太久会挤占其他交互的预算（实测等 1.5s 会把 shop 类挤掉 20 多条）。
	if _, err := page.Eval(armMutationJS); err == nil {
		if _, err := page.Eval(submitFormsJS,
			cfg.LoginConf.Username, cfg.LoginConf.Password,
			cfg.LoginConf.Email, cfg.LoginConf.Phone, formRules); err != nil {
			log.Logger.Debugf("auto submit forms err: %s", err)
		}
		_ = settleAfterClick(page, 600*time.Millisecond)
		collect()
	}

	// 收尾再跑一次冷路径全量扫描。
	// 交互过程中新生成的元素可能带着非标准属性的 URL，热路径扫不到，这里补上。
	collectDeep()

	log.Logger.Debugf("auto collected %d links", len(hrefList))
	return static.HandlerUrls(hrefList, startURL)
}
