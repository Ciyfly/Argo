package inject

// listenerHookJS 在每个新文档起点注入（EvalOnNewDocument，IIFE 格式）。
//
// 对标 crawlergo 的 TabInitJS 并吸收其全部钩子面：
//   - addEventListener 劫持（DOM2）：标记 __argoLsn，供 clickable 收集器发现
//     框架绑定的无结构特征元素（SPA 的 div @click）
//   - onxxx 属性 setter 劫持（DOM0）：el.onclick = fn 这类赋值同样标记
//     （crawlergo 对 20 个 onxxx 全量劫持，这里收交互相关的 6 个）
//   - history.pushState/replaceState + hashchange + popstate：SPA 路由变化
//     在源头捕获进 __argoRoutes（此前靠事后读 location，跳转链会断）
//   - window.close 锁定：页面自杀会杀掉我们的 tab
//   - setInterval 降频到 60s：轮询/动画定时器持续触发 DOM 变化，
//     会把 settleAfterClick 的 changed 判定变成永远 true（crawlergo 同款处理）
//   - XHR 同一 method+url 限 10 次：防无限轮询把浏览器拖忙（crawlergo 同款）
//
// 标记全部用 JS 属性而非 setAttribute：不触发 MutationObserver、
// 不进 HTML 序列化（crawlergo 用属性标记，触发其 DOM 监听反馈环，
// 需要额外摘除监听器来规避——我们的方案天然无此问题）。
const listenerHookJS = `(() => {
	if (window.__argoListenerHooked) { return; }
	window.__argoListenerHooked = true;
	window.__argoRoutes = [];

	// ---- DOM2: addEventListener 劫持，标记 __argoLsn ----
	const originAdd = EventTarget.prototype.addEventListener;
	EventTarget.prototype.addEventListener = function (type, listener, options) {
		try {
			if (this && this.tagName) {
				if (type === "click" || type === "dblclick" || type === "change" || type === "submit") {
					if (!this.__argoLsn) { this.__argoLsn = []; }
					if (this.__argoLsn.indexOf(type) < 0) { this.__argoLsn.push(type); }
				}
			}
		} catch (e) {}
		return originAdd.call(this, type, listener, options);
	};

	// ---- DOM0: onxxx setter 劫持（el.onclick = fn 赋值路径）----
	const dom0Events = ["click", "dblclick", "change", "submit", "mouseover", "mousedown"];
	const props = {};
	dom0Events.forEach(function (name) {
		const attr = "on" + name;
		props[attr] = {
			set: function (newValue) {
				try {
					if (this && this.tagName) {
						if (!this.__argoLsn) { this.__argoLsn = []; }
						if (this.__argoLsn.indexOf(name) < 0) { this.__argoLsn.push(name); }
					}
				} catch (e) {}
				this["_argo_" + attr] = newValue;
			},
			get: function () { return this["_argo_" + attr]; },
			configurable: true,
		};
	});
	try { Object.defineProperties(HTMLElement.prototype, props); } catch (e) {}

	// ---- SPA 路由源头捕获 ----
	function recordRoute(url) {
		try {
			if (url && window.__argoRoutes.indexOf(url) < 0 && window.__argoRoutes.length < 500) {
				window.__argoRoutes.push(String(url));
			}
		} catch (e) {}
	}
	const originPush = history.pushState;
	history.pushState = function (a, b, c) { recordRoute(c); return originPush.apply(this, arguments); };
	const originReplace = history.replaceState;
	history.replaceState = function (a, b, c) { recordRoute(c); return originReplace.apply(this, arguments); };
	window.addEventListener("hashchange", function () { recordRoute(location.href); });
	window.addEventListener("popstate", function () { recordRoute(location.href); });

	// ---- 页面自杀保护 ----
	window.close = function () {};

	// ---- 定时器混合策略（crawlergo 与 katana 哲学的折中）----
	// setTimeout 一次性延时 → 加速 ×0.2：催熟「3 秒后才触发」的延时入口
	//（katana 的 speedUp 思路，基准里的 timed 类用例直接受益）
	// setInterval 循环定时 → 降到 60s：轮询/动画是 DOM 变化噪声源，
	// 会把交互后的页面变化判定变成永真（crawlergo 思路）
	const originTimeout = window.setTimeout;
	window.setTimeout = function () {
		if (typeof arguments[1] === "number" && arguments[1] > 100) {
			arguments[1] = Math.max(1, Math.round(arguments[1] * 0.2));
		}
		return originTimeout.apply(this, arguments);
	};
	const originInterval = window.setInterval;
	window.setInterval = function () {
		arguments[1] = 60000;
		return originInterval.apply(this, arguments);
	};

	// ---- XHR 限次：同一 method+url 最多 10 次 ----
	window.__argoXHRCount = {};
	const originOpen = XMLHttpRequest.prototype.open;
	XMLHttpRequest.prototype.open = function (method, url) {
		this.__argoReqKey = method + " " + url;
		window.__argoXHRCount[this.__argoReqKey] = (window.__argoXHRCount[this.__argoReqKey] || 0) + 1;
		return originOpen.apply(this, arguments);
	};
	const originSend = XMLHttpRequest.prototype.send;
	XMLHttpRequest.prototype.send = function (data) {
		if ((window.__argoXHRCount[this.__argoReqKey] || 0) > 10) { return; }
		return originSend.call(this, data);
	};
})();`

// ListenerHookJS 导出给 engine 注入（page.Eval 用函数格式）。
func ListenerHookJS() string { return listenerHookJS }

// ListenerHookScript 返回脚本源码格式（EvalOnNewDocument 用，IIFE）。
// rod 的 EvalOnNewDocument 把内容当语句执行——直接传 () => {} 只会被求值丢弃，
// 必须包成立即执行（实测踩坑：曾据此误判"环境不支持"）。
func ListenerHookScript() string { return listenerHookJS }
