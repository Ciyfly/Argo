package inject

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/static"
	"argo/pkg/utils"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type autoTemplateConfig struct {
	Username         string   `json:"username"`
	Password         string   `json:"password"`
	Email            string   `json:"email"`
	Phone            string   `json:"phone"`
	Slow             float64  `json:"slow"`
	SlowMin          float64  `json:"slow_min"`
	SlowMax          float64  `json:"slow_max"`
	Highlight        bool     `json:"highlight"`
	Filter           []string `json:"filter"`
	ActionLimit      int      `json:"action_limit"`
	BatchSize        int      `json:"batch_size"`
	CommandTimeoutMs int      `json:"command_timeout_ms"`
}

var AutoJsTemplate = `
function(){
const config = %s;
const sleep = (ms) => new Promise(res => setTimeout(res, ms || 0));
const skipTags = ["HTML","HEAD","META","TITLE","STYLE","SCRIPT"];
let slow = Number(config.slow) >= 0 ? Number(config.slow) : 1000;
const slowMinCfg = Number.isFinite(config.slow_min) ? Math.max(0, Number(config.slow_min)) : null;
const slowMaxCfg = Number.isFinite(config.slow_max) ? Math.max(0, Number(config.slow_max)) : null;
let randomSlowEnabled = slowMinCfg !== null && slowMaxCfg !== null && slowMaxCfg > slowMinCfg;
let randomSlowMin = randomSlowEnabled ? slowMinCfg : slow;
let randomSlowMax = randomSlowEnabled ? slowMaxCfg : slow;
if (!randomSlowEnabled && slowMinCfg !== null && slowMinCfg > 0) {
	slow = slowMinCfg;
	randomSlowMin = slow;
	randomSlowMax = slow;
}
const pickDelay = () => {
	if (randomSlowEnabled) {
		return randomSlowMin + Math.random() * (randomSlowMax - randomSlowMin);
	}
	return slow;
};
const averageSlow = () => {
	if (randomSlowEnabled) {
		return (randomSlowMin + randomSlowMax) / 2;
	}
	return slow;
};
const highlightEnabled = !!config.highlight;
const highlightDuration = 900;
const filter = Array.isArray(config.filter) ? config.filter.filter(Boolean).map(item => String(item).toLowerCase()) : [];
const actionLimit = Number(config.action_limit) > 0 ? Number(config.action_limit) : 200;
const batchSize = Number(config.batch_size) > 0 ? Number(config.batch_size) : 20;
const commandTimeout = Number(config.command_timeout_ms) > 0 ? Number(config.command_timeout_ms) : 800;

// ============ 阶段二: 优先级队列和去重机制 ============
// 优先级队列类
class PriorityQueue {
	constructor() {
		this.high = [];      // 高优先级：弹窗内元素、表单提交、重要链接
		this.normal = [];    // 普通优先级：普通可点击元素
		this.low = [];       // 低优先级：可能是装饰性元素
	}

	enqueue(node, priority = 'normal') {
		switch(priority) {
			case 'high': this.high.push(node); break;
			case 'low': this.low.push(node); break;
			default: this.normal.push(node);
		}
	}

	dequeue() {
		if (this.high.length) return this.high.shift();
		if (this.normal.length) return this.normal.shift();
		if (this.low.length) return this.low.shift();
		return null;
	}

	// 兼容旧接口
	shift() {
		return this.dequeue();
	}

	push(node) {
		this.enqueue(node, 'normal');
	}

	get length() {
		return this.high.length + this.normal.length + this.low.length;
	}

	stats() {
		return {
			high: this.high.length,
			normal: this.normal.length,
			low: this.low.length,
			total: this.length
		};
	}
}

// 元素签名生成（用于去重）
const nodeSignature = (node) => {
	if (!node) return '';
	const tag = node.tagName || '';
	const id = node.id || '';
	const cls = node.className ? (typeof node.className === 'string' ? node.className : '') : '';
	const href = node.getAttribute ? (node.getAttribute('href') || '') : '';
	const text = ((node.innerText || node.textContent || '').trim().slice(0, 30)).replace(/\s+/g, ' ');
	return tag + '#' + id + '.' + cls.split(' ').sort().join('.') + '|' + text + '|' + href;
};

// 去重集合
const seenSignatures = new Set();

// 确定元素优先级
const getPriority = (node) => {
	if (!node || !node.tagName) return 'low';

	const tag = node.tagName;

	// 高优先级
	if (tag === 'A' && node.href && !node.href.startsWith('javascript:')) return 'high';
	if (tag === 'FORM' || (tag === 'INPUT' && node.type === 'submit')) return 'high';
	if (tag === 'BUTTON' && node.type === 'submit') return 'high';
	if (node.closest('[role="dialog"], .modal, .popup, .drawer')) return 'high';

	// 检查是否有明确的点击处理
	if (node.onclick || node.getAttribute('onclick') || node.getAttribute('ng-click')) return 'normal';
	if (hasReactEventHandler(node) || hasVueEventHandler(node)) return 'normal';

	// 低优先级：仅有 cursor:pointer 的元素
	if (!node.onclick && !hasReactEventHandler(node) && !hasVueEventHandler(node)) {
		if (hasPointerCursor(node) && tag !== 'BUTTON' && tag !== 'A') return 'low';
	}

	// 图标类元素低优先级
	if (tag === 'I' || tag === 'SVG' || (node.className && /icon|fa-|material-icons/i.test(node.className))) {
		return 'low';
	}

	return 'normal';
};

// 循环检测
const recentClicks = [];
const MAX_RECENT_CLICKS = 50;

const isClickLoop = (node) => {
	const sig = nodeSignature(node);
	const count = recentClicks.filter(s => s === sig).length;
	return count >= 3; // 同一元素点击超过 3 次视为循环
};

const recordClick = (node) => {
	const sig = nodeSignature(node);
	recentClicks.push(sig);
	if (recentClicks.length > MAX_RECENT_CLICKS) {
		recentClicks.shift();
	}
};

// 深度限制
const MAX_CLICK_DEPTH = 5;
const clickDepthMap = new WeakMap();

const getClickDepth = (node) => {
	let depth = 0;
	let current = node;
	while (current && current !== document.body) {
		if (clickDepthMap.has(current)) {
			depth = Math.max(depth, clickDepthMap.get(current));
		}
		current = current.parentElement;
	}
	return depth;
};

const setClickDepth = (node, depth) => {
	clickDepthMap.set(node, depth);
};

const state = {
	queue: new PriorityQueue(),
	queuedUrls: new Set(),
	visited: new WeakSet(),
	actionLogs: [],
	actions: 0,
	batches: 0,
	stopped: false,
	startTs: performance.now(),
};

// ============ 阶段三: 自适应延迟机制 ============
const adaptiveDelay = {
	baseDelay: slow,
	minDelay: 100,
	maxDelay: 3000,

	// 历史响应时间
	responseTimes: [],
	maxHistory: 20,

	// 网络请求跟踪
	pendingRequests: 0,
	lastNetworkActivity: performance.now(),

	// 记录响应时间
	recordResponse(ms) {
		this.responseTimes.push(ms);
		if (this.responseTimes.length > this.maxHistory) {
			this.responseTimes.shift();
		}
		this.lastNetworkActivity = performance.now();
	},

	// 计算平均响应时间
	getAvgResponse() {
		if (this.responseTimes.length === 0) return this.baseDelay;
		const sum = this.responseTimes.reduce((a, b) => a + b, 0);
		return sum / this.responseTimes.length;
	},

	// 获取自适应延迟
	getDelay() {
		const avgResponse = this.getAvgResponse();

		// 有待处理请求时增加延迟
		if (this.pendingRequests > 0) {
			return Math.min(avgResponse * 2 + 200, this.maxDelay);
		}

		// 根据平均响应时间调整
		let delay = avgResponse * 1.2;

		// 如果响应很快，可以减少延迟
		if (avgResponse < 200 && this.responseTimes.length >= 5) {
			delay = Math.max(avgResponse * 0.8, this.minDelay);
		}

		// 确保在范围内
		delay = Math.max(delay, this.minDelay);
		delay = Math.min(delay, this.maxDelay);

		// 添加随机抖动 ±10%
		const jitter = delay * 0.1;
		delay += Math.random() * jitter * 2 - jitter;

		return Math.round(delay);
	},

	// 检查网络是否空闲
	isNetworkIdle() {
		return this.pendingRequests === 0;
	},

	// 等待网络空闲
	async waitForNetworkIdle(timeout = 2000) {
		const start = performance.now();
		while (this.pendingRequests > 0) {
			if (performance.now() - start > timeout) break;
			await sleep(50);
		}
		// 额外短暂等待确保 DOM 更新
		await sleep(50);
	},

	// 获取统计信息
	stats() {
		return {
			avg_response_ms: Math.round(this.getAvgResponse()),
			current_delay_ms: this.getDelay(),
			pending_requests: this.pendingRequests,
			sample_count: this.responseTimes.length,
		};
	}
};

// 增强 fetch 跟踪网络状态
const originalFetchForTracking = window.fetch;
window.fetch = async function(...args) {
	adaptiveDelay.pendingRequests++;
	const start = performance.now();
	try {
		const result = await originalFetchForTracking.apply(this, args);
		adaptiveDelay.recordResponse(performance.now() - start);
		return result;
	} catch(err) {
		adaptiveDelay.recordResponse(performance.now() - start);
		throw err;
	} finally {
		adaptiveDelay.pendingRequests--;
	}
};

// 增强 XHR 跟踪网络状态
const originalXHRSend = XMLHttpRequest.prototype.send;
XMLHttpRequest.prototype.send = function(...args) {
	adaptiveDelay.pendingRequests++;
	const start = performance.now();
	const xhr = this;

	const onComplete = () => {
		adaptiveDelay.recordResponse(performance.now() - start);
		adaptiveDelay.pendingRequests--;
	};

	xhr.addEventListener('load', onComplete, {once: true});
	xhr.addEventListener('error', onComplete, {once: true});
	xhr.addEventListener('abort', onComplete, {once: true});
	xhr.addEventListener('timeout', onComplete, {once: true});

	return originalXHRSend.apply(this, args);
};

// 自适应延迟的 pickDelay 函数
const pickDelayAdaptive = () => {
	// 如果启用了随机延迟配置，保持兼容
	if (randomSlowEnabled) {
		const configDelay = randomSlowMin + Math.random() * (randomSlowMax - randomSlowMin);
		const adaptiveVal = adaptiveDelay.getDelay();
		// 取两者的较大值，确保不会太快
		return Math.max(configDelay, adaptiveVal * 0.5);
	}
	return adaptiveDelay.getDelay();
};

function initBridge() {
	const hasEmitter = typeof window.__argoBridgeEmit === "function";
	if (!hasEmitter) {
		return {
			enabled: false,
			emit: () => {},
			waitCommand: () => Promise.resolve(null),
		};
	}
	const pending = [];
	const waiters = [];
	const bridge = {
		enabled: true,
		emit(event, payload) {
			try {
				window.__argoBridgeEmit({event, payload});
			} catch(err) {
				console.log("argo emit error", err);
			}
		},
		waitCommand(timeout) {
			return new Promise(resolve => {
				if (pending.length > 0) {
					return resolve(pending.shift());
				}
				let timer;
				const done = (cmd) => {
					if (timer) clearTimeout(timer);
					resolve(cmd || null);
				};
				waiters.push(done);
				if (timeout > 0) {
					timer = setTimeout(() => {
						const idx = waiters.indexOf(done);
						if (idx >= 0) {
							waiters.splice(idx, 1);
						}
						resolve(null);
					}, timeout);
				}
			});
		},
		pushCommand(cmd) {
			if (waiters.length > 0) {
				const fn = waiters.shift();
				fn(cmd);
				return;
			}
			pending.push(cmd);
		},
	};
	window.__argoBridgePushCommand = (payload) => {
		try {
			const parsed = typeof payload === "string" ? JSON.parse(payload) : payload;
			bridge.pushCommand(parsed);
		} catch(err) {
			console.log("argo command parse error", err);
		}
	};
	bridge.emit("bridge_ready", {ts: Date.now()});
	return bridge;
}

const bridge = initBridge();

// ============ History API 拦截 - 捕获 SPA 路由变化 ============
const originalPushState = history.pushState;
const originalReplaceState = history.replaceState;

history.pushState = function(state, title, url) {
	const result = originalPushState.apply(this, arguments);
	if (url) {
		try {
			const absoluteUrl = new URL(url, window.location.href).href;
			enqueueUrl(absoluteUrl);
		} catch(e) {
			enqueueUrl(url);
		}
	}
	return result;
};

history.replaceState = function(state, title, url) {
	const result = originalReplaceState.apply(this, arguments);
	if (url) {
		try {
			const absoluteUrl = new URL(url, window.location.href).href;
			enqueueUrl(absoluteUrl);
		} catch(e) {
			enqueueUrl(url);
		}
	}
	return result;
};

// 监听 popstate 和 hashchange 事件
window.addEventListener('popstate', function(event) {
	enqueueUrl(window.location.href);
});

window.addEventListener('hashchange', function(event) {
	enqueueUrl(window.location.href);
});

// ============ XHR/Fetch 拦截 - 捕获动态请求 ============
const originalFetch = window.fetch;
window.fetch = function(input, init) {
	try {
		let url = typeof input === 'string' ? input : (input && input.url ? input.url : '');
		if (url && !url.startsWith('data:') && !url.startsWith('blob:')) {
			try {
				const absoluteUrl = new URL(url, window.location.href).href;
				enqueueUrl(absoluteUrl);
			} catch(e) {
				enqueueUrl(url);
			}
		}
	} catch(err) {
		// 忽略错误
	}
	return originalFetch.apply(this, arguments);
};

const originalXHROpen = XMLHttpRequest.prototype.open;
XMLHttpRequest.prototype.open = function(method, url) {
	try {
		if (url && !url.startsWith('data:') && !url.startsWith('blob:')) {
			try {
				const absoluteUrl = new URL(url, window.location.href).href;
				enqueueUrl(absoluteUrl);
			} catch(e) {
				enqueueUrl(url);
			}
		}
	} catch(err) {
		// 忽略错误
	}
	return originalXHROpen.apply(this, arguments);
};

// ============ WebSocket 拦截 - 捕获 WebSocket URL ============
const originalWebSocket = window.WebSocket;
window.WebSocket = function(url, protocols) {
	try {
		if (url) {
			// 将 ws:// 或 wss:// 转换为 http:// 或 https://
			let httpUrl = url.replace(/^ws:/, 'http:').replace(/^wss:/, 'https:');
			enqueueUrl(httpUrl);
		}
	} catch(err) {
		// 忽略错误
	}
	return new originalWebSocket(url, protocols);
};
window.WebSocket.prototype = originalWebSocket.prototype;
window.WebSocket.CONNECTING = originalWebSocket.CONNECTING;
window.WebSocket.OPEN = originalWebSocket.OPEN;
window.WebSocket.CLOSING = originalWebSocket.CLOSING;
window.WebSocket.CLOSED = originalWebSocket.CLOSED;

// ============ 原有代码 ============
const shouldSkip = (node) => {
	if (!node || !node.tagName) return true;
	if (state.visited.has(node)) return true;
	return skipTags.indexOf(node.tagName) >= 0;
};

// 增强的入队函数（带去重和优先级）
const enqueueNode = (node) => {
	if (shouldSkip(node)) return;

	// 签名去重
	const sig = nodeSignature(node);
	if (sig && seenSignatures.has(sig)) return;

	// 深度检查
	const depth = getClickDepth(node);
	if (depth >= MAX_CLICK_DEPTH) return;

	state.visited.add(node);
	if (sig) seenSignatures.add(sig);

	// 确定优先级并入队
	const priority = getPriority(node);
	state.queue.enqueue(node, priority);
};

// 高优先级入队（用于弹窗内元素等）
const enqueueNodeHighPriority = (node) => {
	if (shouldSkip(node)) return;

	const sig = nodeSignature(node);
	if (sig && seenSignatures.has(sig)) return;

	state.visited.add(node);
	if (sig) seenSignatures.add(sig);

	state.queue.enqueue(node, 'high');
};

const enqueueUrl = (url) => {
	if (!url) return;
	state.queuedUrls.add(url);
};

const matchesFilter = (node) => {
	if (!node) return false;
	const html = node.outerHTML ? node.outerHTML.toLowerCase() : "";
	return filter.some(f => html.includes(f));
};

const triggerInput = (node, value) => {
	node && node.focus && node.focus();
	if (!node) return;
	node.value = value;
	node.dispatchEvent(new Event("input", {bubbles:true}));
	node.dispatchEvent(new Event("change", {bubbles:true}));
};

const hasPointerCursor = (node) => {
	try {
		const style = node ? window.getComputedStyle(node) : null;
		return style && style.cursor && style.cursor.toLowerCase().includes("pointer");
	} catch(err) {
		return false;
	}
};

const hasDataClickAttr = (node) => {
	if (!node || !node.getAttributeNames) return false;
	const names = node.getAttributeNames();
	return names.some(name => {
		if (!name) return false;
		const lower = name.toLowerCase();
		return lower === "data-click"
			|| lower === "data-action"
			|| lower === "data-event"
			|| lower.startsWith("@click")
			|| lower.startsWith("data-on");
	});
};

// 检测 React Fiber 属性（React 16+）
const hasReactEventHandler = (node) => {
	if (!node) return false;
	// React Fiber 将事件处理器存储在以 __reactProps$ 或 __reactFiber$ 开头的属性中
	const keys = Object.keys(node);
	for (let i = 0; i < keys.length; i++) {
		const key = keys[i];
		// React 16+ Fiber 属性
		if (key.startsWith('__reactProps$') || key.startsWith('__reactFiber$')) {
			const props = node[key];
			if (props && (props.onClick || props.onMouseDown || props.onPointerDown || props.onTouchStart)) {
				return true;
			}
		}
		// React 15 及更早版本
		if (key.startsWith('__reactInternalInstance$')) {
			const instance = node[key];
			if (instance && instance._currentElement && instance._currentElement.props) {
				const props = instance._currentElement.props;
				if (props.onClick || props.onMouseDown || props.onPointerDown) {
					return true;
				}
			}
		}
	}
	// 检查 React 事件监听器（新版本）
	if (node._reactListeners || node._reactEvents) return true;
	return false;
};

// 检测 Vue 事件处理器
const hasVueEventHandler = (node) => {
	if (!node) return false;
	// Vue 2.x 使用 __vue__ 属性
	if (node.__vue__) {
		const vm = node.__vue__;
		if (vm.$listeners && (vm.$listeners.click || vm.$listeners.tap)) return true;
		if (vm._events && (vm._events.click || vm._events.tap)) return true;
	}
	// Vue 3.x 使用 __vueParentComponent
	if (node.__vueParentComponent) {
		const component = node.__vueParentComponent;
		if (component.props) {
			const props = component.props;
			if (props.onClick || props['onClick'] || props.onTap) return true;
		}
	}
	// 检查 Vue 指令绑定
	if (node.__vue_bindings__) {
		const bindings = node.__vue_bindings__;
		if (bindings.click || bindings.tap) return true;
	}
	return false;
};

// 检测通过 addEventListener 绑定的 click 事件
const hasAddedEventListener = (node) => {
	if (!node) return false;
	// 某些浏览器暴露 getEventListeners（仅在 DevTools 中）
	// 这里使用间接检测方法：检查元素是否有事件相关标记
	if (node._events || node.__events || node.__listeners) return true;
	// jQuery 事件
	if (typeof jQuery !== 'undefined' && jQuery._data) {
		const events = jQuery._data(node, 'events');
		if (events && (events.click || events.mousedown || events.tap)) return true;
	}
	// 检查常见的事件存储属性
	if (node.eventListeners && typeof node.eventListeners === 'object') return true;
	return false;
};

const isClickable = (node) => {
	if (!node) return false;
	const tag = node.tagName;
	// 原生可点击元素
	if (tag === "BUTTON") return true;
	if (tag === "A") return true;
	if (tag === "INPUT" && ["button","submit","image","reset"].includes(node.type)) return true;
	if (tag === "SELECT") return true;
	if (tag === "LABEL") return true;
	if (tag === "SUMMARY") return true; // details 元素的展开按钮

	// ARIA 角色
	const role = node.getAttribute("role");
	if (role && ["button","link","menuitem","tab","checkbox","radio","switch","option"].includes(role)) return true;

	// 原生 onclick 属性
	if (node.onclick || node.getAttribute("onclick")) return true;

	// Angular 事件
	if (node.getAttribute("ng-click") || node.getAttribute("(click)")) return true;

	// Vue 模板事件 (@click, v-on:click)
	if (node.getAttribute("@click") || node.getAttribute("v-on:click")) return true;

	// React/Vue 框架内部事件检测
	if (hasReactEventHandler(node)) return true;
	if (hasVueEventHandler(node)) return true;

	// addEventListener 绑定检测
	if (hasAddedEventListener(node)) return true;

	// tabIndex + pointer cursor（可交互元素）
	if (node.tabIndex >= 0 && hasPointerCursor(node)) return true;

	// 仅有 pointer cursor 也可能是可点击的
	if (hasPointerCursor(node)) return true;

	// 自定义 data 属性
	if (hasDataClickAttr(node)) return true;

	// contenteditable 元素
	if (node.contentEditable === "true") return true;

	// draggable 元素
	if (node.draggable) return true;

	return false;
};

const highlightNode = (node) => {
	if (!highlightEnabled || !node || !node.getBoundingClientRect) return;
	const rect = node.getBoundingClientRect();
	if (!rect || rect.width === 0 || rect.height === 0 || !document.body) return;
	const overlay = document.createElement("div");
	overlay.className = "__argo-click-highlight";
	Object.assign(overlay.style, {
		position: "fixed",
		left: rect.left + "px",
		top: rect.top + "px",
		width: rect.width + "px",
		height: rect.height + "px",
		border: "2px solid #ff1744",
		borderRadius: "4px",
		boxShadow: "0 0 10px rgba(255, 23, 68, 0.8)",
		background: "rgba(255, 23, 68, 0.1)",
		pointerEvents: "none",
		zIndex: 2147483647,
		transition: "opacity 0.3s ease-out",
		opacity: "1",
	});
	document.body.appendChild(overlay);
	requestAnimationFrame(() => { overlay.style.opacity = "0"; });
	setTimeout(() => { overlay.remove(); }, highlightDuration);
};

const getNodeText = (node) => {
	if (!node) return "";
	const content = (node.innerText || node.textContent || "").replace(/\s+/g, " ").trim();
	return content.slice(0, 80);
};

const buildSelector = (node) => {
	if (!node) return "";
	let selector = node.tagName ? node.tagName.toLowerCase() : "";
	if (node.id) {
		selector += "#" + node.id;
	}
	if (node.classList && node.classList.length) {
		selector += "." + Array.from(node.classList).map(cls => cls.trim()).filter(Boolean).join(".");
	}
	return selector || (node.outerHTML ? node.outerHTML.slice(0, 80) : "");
};

const logClick = (node, reason) => {
	state.actionLogs.push({
		tag: node && node.tagName,
		text: getNodeText(node),
		selector: buildSelector(node),
		reason: reason || "auto-click",
	});
};

const clickNode = async (node, reason) => {
	if (!node || matchesFilter(node)) return;
	const style = node ? window.getComputedStyle(node) : null;
	if (style && style.visibility === "hidden") return;
	if (style && style.display === "none") return;
	highlightNode(node);
	logClick(node, reason);
	node.focus && node.focus();
	node.dispatchEvent(new MouseEvent("pointerdown", {bubbles:true}));
	node.dispatchEvent(new MouseEvent("mousedown", {bubbles:true}));
	node.click && node.click();
	node.dispatchEvent(new MouseEvent("mouseup", {bubbles:true}));
	node.dispatchEvent(new MouseEvent("pointerup", {bubbles:true}));
		await sleep(pickDelay());
};

const processNode = async (node) => {
	if (!node) return;

	// 循环检测
	if (isClickLoop(node)) {
		bridge.emit("click_loop_detected", {selector: buildSelector(node)});
		return;
	}

	// 深度检查
	const depth = getClickDepth(node);
	if (depth >= MAX_CLICK_DEPTH) {
		bridge.emit("depth_limit_reached", {selector: buildSelector(node), depth});
		return;
	}

	const href = node.getAttribute && node.getAttribute("href");
	if (href && !href.startsWith("javascript") && href !== "#") {
		enqueueUrl(href);
	}
	if (node.tagName === "INPUT") {
		const type = (node.type || "text").toLowerCase();
		if (["text","search","url","password"].includes(type)) {
			triggerInput(node, config.username);
		} else if (type === "email") {
			triggerInput(node, config.email);
		} else if (type === "tel") {
			triggerInput(node, config.phone);
		}
	}
	if (isClickable(node)) {
		// 记录点击
		recordClick(node);
		// 更新深度
		setClickDepth(node, depth + 1);
		// 使用增强的点击函数（包含弹窗检测）
		await clickNodeWithPopupCheck(node, "auto-click");
	}
};

const observer = new MutationObserver((mutations) => {
	mutations.forEach((mutation) => {
		mutation.addedNodes && mutation.addedNodes.forEach(enqueueNode);
	});
});
observer.observe(document, {subtree:true, childList:true});

// ============ 阶段四: Shadow DOM / iframe 支持 ============

// 递归遍历 Shadow DOM
const walkShadowDom = (root, callback) => {
	if (!root) return;
	const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT, null);
	while (walker.nextNode()) {
		const node = walker.currentNode;
		callback(node);

		// 检查并遍历 Shadow DOM
		if (node.shadowRoot) {
			walkShadowDom(node.shadowRoot, callback);
		}
	}
};

// 处理 iframe（仅同源）
const processIframes = () => {
	const iframes = document.querySelectorAll('iframe');
	let processedCount = 0;

	for (const iframe of iframes) {
		try {
			// 检查是否可访问（同源策略）
			const doc = iframe.contentDocument || (iframe.contentWindow && iframe.contentWindow.document);
			if (doc && doc.body) {
				// 遍历 iframe 内的 DOM
				walkShadowDom(doc.body, (node) => {
					enqueueNode(node);
				});
				processedCount++;
			}
		} catch(e) {
			// 跨域 iframe，忽略
		}
	}

	if (processedCount > 0) {
		bridge.emit("iframes_processed", {count: processedCount});
	}
};

// 扫描 Shadow DOM 中的元素
const scanShadowRoots = () => {
	let shadowCount = 0;
	const allElements = document.querySelectorAll('*');

	for (const el of allElements) {
		if (el.shadowRoot) {
			walkShadowDom(el.shadowRoot, (node) => {
				enqueueNode(node);
				shadowCount++;
			});
		}
	}

	if (shadowCount > 0) {
		bridge.emit("shadow_dom_scanned", {elements_found: shadowCount});
	}
};

// 监听 Shadow Root 的添加
const originalAttachShadow = Element.prototype.attachShadow;
Element.prototype.attachShadow = function(options) {
	const shadowRoot = originalAttachShadow.call(this, options);

	// 延迟扫描新创建的 Shadow DOM
	setTimeout(() => {
		walkShadowDom(shadowRoot, enqueueNode);
	}, 100);

	// 为 Shadow Root 添加 MutationObserver
	const shadowObserver = new MutationObserver((mutations) => {
		mutations.forEach((mutation) => {
			mutation.addedNodes && mutation.addedNodes.forEach((node) => {
				if (node.nodeType === Node.ELEMENT_NODE) {
					enqueueNode(node);
					// 检查新节点是否有 Shadow DOM
					if (node.shadowRoot) {
						walkShadowDom(node.shadowRoot, enqueueNode);
					}
				}
			});
		});
	});
	shadowObserver.observe(shadowRoot, {subtree: true, childList: true});

	return shadowRoot;
};

// 初始 DOM 扫描（包含 Shadow DOM）
walkShadowDom(document, enqueueNode);

// 扫描现有的 Shadow Roots
scanShadowRoots();

// 处理 iframe
processIframes();

// ============ 阶段一: 增强弹窗处理 ============
const popupKeywords = ["modal","popup","dialog","captcha","mask","overlay","float","drawer","toast","alert","confirm","lightbox","layer"];
const closeSelectors = [
	"[aria-label*=close]",
	"[aria-label*=Close]",
	"[aria-label*=关闭]",
	"[data-testid*=close]",
	"[data-dismiss]",
	"[data-close]",
	".btn-close",
	".close-btn",
	".close-button",
	".icon-close",
	".modal-close",
	".popup-close",
	".dialog-close",
	"button[class*=close]",
	"a[class*=close]",
	"span[class*=close]",
	"i[class*=close]",
	".ant-modal-close",
	".el-dialog__close",
	".el-message-box__close",
	".ivu-modal-close",
	".van-popup__close-icon",
	".layui-layer-close",
	"[class*=dismiss]",
	"[class*=cancel]"
];

// 弹窗状态管理
const popupState = {
	activePopups: new Map(),     // 当前活跃弹窗 node -> info
	closedPopups: new WeakSet(), // 已关闭的弹窗
	processedInPopup: new WeakSet(), // 弹窗内已处理的元素
	popupQueue: [],              // 弹窗内高优先级元素队列
	lastCheck: 0,
};

// 检测元素是否可见
const isElementVisible = (node) => {
	if (!node || !node.getBoundingClientRect) return false;
	const style = window.getComputedStyle(node);
	if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false;
	const rect = node.getBoundingClientRect();
	return rect.width > 0 && rect.height > 0;
};

// 检测是否是关闭按钮
const isCloseButton = (node) => {
	if (!node) return false;
	const text = (node.innerText || node.textContent || '').toLowerCase().trim();
	const closeTexts = ['×', 'x', 'close', '关闭', '取消', 'cancel', 'dismiss'];
	if (closeTexts.some(t => text === t || text.startsWith(t))) return true;

	const attrs = (node.className || '') + ' ' + (node.id || '') + ' ' + (node.getAttribute('aria-label') || '');
	if (/close|dismiss|cancel|关闭/i.test(attrs)) return true;

	// 检查是否匹配关闭选择器
	for (const selector of closeSelectors) {
		try {
			if (node.matches(selector)) return true;
		} catch(e) {}
	}
	return false;
};

// 增强的弹窗检测
const looksLikePopup = (node) => {
	if (!node || node === document.body || node === document.documentElement) return false;
	if (node.tagName === "SCRIPT" || node.tagName === "STYLE" || node.tagName === "LINK") return false;
	if (popupState.closedPopups.has(node)) return false;

	try {
		const style = window.getComputedStyle(node);
		if (!style) return false;

		// 必须可见
		if (style.display === 'none' || style.visibility === 'hidden') return false;

		const position = style.position || "";
		const zIndex = parseInt(style.zIndex) || 0;

		// 高 z-index 的 fixed/absolute 元素
		if (zIndex > 100 && ["fixed","absolute"].includes(position)) return true;

		// 检查 ARIA 属性
		if (node.getAttribute("role") === "dialog" || node.getAttribute("aria-modal") === "true") return true;

		// 检查位置和覆盖面积
		if (!["fixed","absolute","sticky"].includes(position)) return false;

		const rect = node.getBoundingClientRect();
		if (!rect || rect.width === 0 || rect.height === 0) return false;

		const area = rect.width * rect.height;
		const viewportArea = window.innerWidth * window.innerHeight;
		const coverage = viewportArea > 0 ? (area / viewportArea) : 0;

		// 大面积覆盖
		if (coverage > 0.15) return true;

		// 检查关键字
		const attrs = ((node.className || "") + " " + (node.id || "")).toLowerCase();
		if (popupKeywords.some(k => attrs.includes(k))) return true;

		// 检查背景遮罩特征
		const bg = style.backgroundColor || '';
		if (bg.includes('rgba') && coverage > 0.5) {
			const alpha = parseFloat(bg.split(',')[3]) || 0;
			if (alpha > 0.2 && alpha < 1) return true; // 半透明遮罩
		}

		return false;
	} catch(err) {
		return false;
	}
};

// 在弹窗内查找关闭按钮
const findCloseButton = (popup) => {
	// 按优先级尝试各种选择器
	for (const selector of closeSelectors) {
		try {
			const btn = popup.querySelector(selector);
			if (btn && isElementVisible(btn)) return btn;
		} catch(e) {}
	}

	// 查找包含 X 或 × 的小按钮
	const allButtons = popup.querySelectorAll('button, a, span, i, svg');
	for (const btn of allButtons) {
		if (!isElementVisible(btn)) continue;
		const text = (btn.innerText || btn.textContent || '').trim();
		if (text === '×' || text === 'X' || text === 'x' || text === '✕' || text === '✖') {
			return btn;
		}
	}

	return null;
};

// 提取弹窗内的有价值元素
const extractPopupContent = (popup) => {
	const urls = [];
	const clickables = [];

	// 提取链接
	const links = popup.querySelectorAll('a[href]');
	for (const link of links) {
		if (isCloseButton(link)) continue;
		const href = link.getAttribute('href');
		if (href && !href.startsWith('javascript:') && href !== '#') {
			urls.push(href);
		}
	}

	// 提取可点击元素（非关闭按钮）
	const interactive = popup.querySelectorAll('button, [role="button"], input[type="submit"], a');
	for (const el of interactive) {
		if (isCloseButton(el)) continue;
		if (!isElementVisible(el)) continue;
		if (!popupState.processedInPopup.has(el)) {
			clickables.push(el);
			popupState.processedInPopup.add(el);
		}
	}

	return { urls, clickables };
};

// 等待 DOM 稳定（短时间版本）
const waitForDomStableShort = (maxWait = 500) => {
	return new Promise(resolve => {
		let lastCount = document.querySelectorAll('*').length;
		let stableChecks = 0;
		const startTime = performance.now();

		const check = () => {
			const currentCount = document.querySelectorAll('*').length;
			if (currentCount === lastCount) {
				stableChecks++;
				if (stableChecks >= 2) {
					resolve();
					return;
				}
			} else {
				stableChecks = 0;
				lastCount = currentCount;
			}

			if (performance.now() - startTime > maxWait) {
				resolve();
				return;
			}

			setTimeout(check, 100);
		};
		check();
	});
};

// 智能关闭弹窗
const closePopupSmart = async (popup) => {
	if (popupState.closedPopups.has(popup)) return;

	// 1. 提取弹窗内容
	const { urls, clickables } = extractPopupContent(popup);

	// 记录发现的 URL
	urls.forEach(url => {
		try {
			const absoluteUrl = new URL(url, window.location.href).href;
			enqueueUrl(absoluteUrl);
		} catch(e) {
			enqueueUrl(url);
		}
	});

	// 将弹窗内可点击元素加入高优先级队列
	clickables.forEach(el => {
		popupState.popupQueue.push(el);
	});

	bridge.emit("popup_content", {
		urls_found: urls.length,
		clickables_found: clickables.length,
		selector: buildSelector(popup),
	});

	// 2. 尝试关闭弹窗
	const closeBtn = findCloseButton(popup);
	if (closeBtn) {
		// 点击关闭按钮
		closeBtn.dispatchEvent(new MouseEvent("click", {bubbles: true}));
		await sleep(200);

		// 检查是否关闭成功
		if (!isElementVisible(popup)) {
			popupState.closedPopups.add(popup);
			state.actionLogs.push({
				tag: "POPUP",
				selector: buildSelector(popup),
				text: getNodeText(popup).slice(0, 50),
				reason: "closed-by-button",
			});
			return;
		}
	}

	// 3. 尝试 ESC 键关闭
	document.dispatchEvent(new KeyboardEvent('keydown', {
		key: 'Escape',
		code: 'Escape',
		keyCode: 27,
		which: 27,
		bubbles: true
	}));
	await sleep(200);

	if (!isElementVisible(popup)) {
		popupState.closedPopups.add(popup);
		state.actionLogs.push({
			tag: "POPUP",
			selector: buildSelector(popup),
			text: getNodeText(popup).slice(0, 50),
			reason: "closed-by-escape",
		});
		return;
	}

	// 4. 点击遮罩层关闭（如果弹窗外有遮罩）
	const parent = popup.parentElement;
	if (parent && looksLikePopup(parent)) {
		parent.dispatchEvent(new MouseEvent("click", {bubbles: false}));
		await sleep(200);
	}

	// 5. 强制隐藏
	if (isElementVisible(popup)) {
		popup.style.setProperty("display", "none", "important");
		popup.style.setProperty("visibility", "hidden", "important");
		popup.style.setProperty("opacity", "0", "important");
		popupState.closedPopups.add(popup);
		state.actionLogs.push({
			tag: "POPUP",
			selector: buildSelector(popup),
			text: getNodeText(popup).slice(0, 50),
			reason: "force-hidden",
		});
	}
};

// 检测所有当前弹窗
const detectAllPopups = () => {
	const popups = [];
	const allElements = document.querySelectorAll('body *');
	for (const el of allElements) {
		if (looksLikePopup(el)) {
			// 避免重复检测嵌套弹窗
			let isNested = false;
			for (const existing of popups) {
				if (existing.contains(el) || el.contains(existing)) {
					isNested = true;
					break;
				}
			}
			if (!isNested) {
				popups.push(el);
			}
		}
	}
	return popups;
};

// 点击后检测弹窗
const checkAndHandlePopups = async () => {
	const popups = detectAllPopups();
	for (const popup of popups) {
		if (!popupState.closedPopups.has(popup)) {
			await closePopupSmart(popup);
			await waitForDomStableShort(300);
		}
	}
};

// 增强的点击处理（包含弹窗检测和自适应延迟）
const clickNodeWithPopupCheck = async (node, reason) => {
	if (!node || matchesFilter(node)) return false;

	const style = window.getComputedStyle(node);
	if (style && (style.visibility === "hidden" || style.display === "none")) return false;

	// 检查元素是否在视口内，不在则滚动
	const rect = node.getBoundingClientRect();
	if (rect.top < 0 || rect.bottom > window.innerHeight || rect.left < 0 || rect.right > window.innerWidth) {
		node.scrollIntoView({ behavior: 'instant', block: 'center', inline: 'center' });
		await sleep(100);
	}

	// 记录点击前的弹窗状态
	const beforePopups = new Set(detectAllPopups());
	const clickStart = performance.now();

	// 模拟更真实的用户行为
	node.dispatchEvent(new MouseEvent("mouseover", {bubbles: true}));
	await sleep(30);
	node.dispatchEvent(new MouseEvent("mouseenter", {bubbles: true}));

	// 执行点击
	highlightNode(node);
	logClick(node, reason);

	node.focus && node.focus();
	node.dispatchEvent(new MouseEvent("pointerdown", {bubbles:true}));
	node.dispatchEvent(new MouseEvent("mousedown", {bubbles:true}));

	await sleep(30 + Math.random() * 50); // 模拟按下时间

	node.click && node.click();
	node.dispatchEvent(new MouseEvent("mouseup", {bubbles:true}));
	node.dispatchEvent(new MouseEvent("pointerup", {bubbles:true}));

	// 等待网络请求完成
	await adaptiveDelay.waitForNetworkIdle(1500);

	// 等待 DOM 稳定
	await waitForDomStableShort(400);

	// 检测新弹窗
	const afterPopups = detectAllPopups();
	const newPopups = afterPopups.filter(p => !beforePopups.has(p));

	if (newPopups.length > 0) {
		bridge.emit("popup_detected", {
			count: newPopups.length,
			trigger: buildSelector(node),
		});

		for (const popup of newPopups) {
			await closePopupSmart(popup);
		}
	}

	// 记录本次操作的响应时间
	const clickDuration = performance.now() - clickStart;
	adaptiveDelay.recordResponse(clickDuration);

	// 自适应延迟
	const delay = pickDelayAdaptive();
	await sleep(delay);

	// 定期输出自适应延迟统计
	if (state.actions % 10 === 0) {
		bridge.emit("adaptive_delay_stats", adaptiveDelay.stats());
	}

	return true;
};

// 处理弹窗队列中的元素
const processPopupQueue = async () => {
	while (popupState.popupQueue.length > 0 && state.actions < actionLimit) {
		const el = popupState.popupQueue.shift();
		if (!el || !document.contains(el) || !isElementVisible(el)) continue;

		if (isClickable(el)) {
			await clickNodeWithPopupCheck(el, "popup-element");
			state.actions++;
		}
	}
};

// 被动弹窗观察器（用于异步出现的弹窗）
const popupObserver = new MutationObserver(async (mutations) => {
	// 限制检查频率
	const now = performance.now();
	if (now - popupState.lastCheck < 500) return;
	popupState.lastCheck = now;

	for (const mutation of mutations) {
		for (const node of mutation.addedNodes) {
			if (node.nodeType === Node.ELEMENT_NODE) {
				if (looksLikePopup(node)) {
					await closePopupSmart(node);
				}
				// 检查子元素
				if (node.querySelectorAll) {
					const childPopups = Array.from(node.querySelectorAll('*')).filter(looksLikePopup);
					for (const popup of childPopups) {
						await closePopupSmart(popup);
					}
				}
			}
		}
	}
});

popupObserver.observe(document.body || document.documentElement, {
	subtree: true,
	childList: true
});

// 初始清理弹窗
(async () => {
	const initialPopups = detectAllPopups();
	for (const popup of initialPopups) {
		await closePopupSmart(popup);
	}
})();

// ============ 异步渲染等待机制 ============
// 等待 Vue/React 框架完成初始渲染
const waitForFrameworkReady = async () => {
	const maxWait = 3000; // 最大等待 3 秒
	const checkInterval = 100;
	const startTime = performance.now();

	// 检测框架是否存在并完成加载
	const isFrameworkReady = () => {
		// Vue 2.x 检测
		if (window.Vue && window.Vue.version) {
			// 检查是否有挂载的 Vue 实例
			const vueApps = document.querySelectorAll('[data-v-app], [data-server-rendered], .__vue_root__');
			if (vueApps.length > 0) return true;
			// 检查 body 下是否有 __vue__ 属性的元素
			const vueElements = document.querySelectorAll('*');
			for (let el of vueElements) {
				if (el.__vue__) return true;
			}
		}
		// Vue 3.x 检测
		if (window.__VUE__) {
			const vue3Apps = document.querySelectorAll('[data-v-app]');
			if (vue3Apps.length > 0) return true;
		}
		// React 检测
		if (window.React || window.__REACT_DEVTOOLS_GLOBAL_HOOK__) {
			// 检查是否有 React root
			const reactRoots = document.querySelectorAll('[data-reactroot], #root, #app, #__next');
			for (let root of reactRoots) {
				const keys = Object.keys(root);
				for (let key of keys) {
					if (key.startsWith('__reactContainer$') || key.startsWith('__reactFiber$')) {
						return true;
					}
				}
			}
		}
		// Next.js 检测
		if (window.__NEXT_DATA__) return true;
		// Nuxt.js 检测
		if (window.__NUXT__) return true;
		// Angular 检测
		if (window.ng || window.getAllAngularRootElements) return true;
		// Svelte 检测
		if (document.querySelector('[class*="svelte"]')) return true;
		// 没有检测到框架，假设已就绪
		return true;
	};

	// 等待 DOM 稳定（没有新节点添加）
	const waitForDomStable = () => {
		return new Promise((resolve) => {
			let lastNodeCount = document.querySelectorAll('*').length;
			let stableCount = 0;
			const checkStable = () => {
				const currentCount = document.querySelectorAll('*').length;
				if (currentCount === lastNodeCount) {
					stableCount++;
					if (stableCount >= 3) { // 连续 3 次检查稳定
						resolve();
						return;
					}
				} else {
					stableCount = 0;
					lastNodeCount = currentCount;
				}
				if (performance.now() - startTime > maxWait) {
					resolve();
					return;
				}
				setTimeout(checkStable, checkInterval);
			};
			checkStable();
		});
	};

	// 等待网络请求完成（简单版本）
	const waitForNetworkIdle = () => {
		return new Promise((resolve) => {
			// 检查是否有 pending 的 fetch/XHR
			// 由于我们已经 hook 了 fetch 和 XHR，这里只是一个简单的延迟
			setTimeout(resolve, 200);
		});
	};

	// 首先等待框架就绪
	while (!isFrameworkReady() && (performance.now() - startTime) < maxWait) {
		await sleep(checkInterval);
	}

	// 然后等待 DOM 稳定
	await Promise.race([
		waitForDomStable(),
		sleep(maxWait - (performance.now() - startTime))
	]);

	// 最后等待网络空闲
	await waitForNetworkIdle();

	bridge.emit("framework_ready", {
		wait_time_ms: Math.round(performance.now() - startTime),
		has_vue: !!window.Vue || !!window.__VUE__,
		has_react: !!window.React || !!window.__REACT_DEVTOOLS_GLOBAL_HOOK__,
		has_angular: !!window.ng,
	});
};

// 重新扫描 DOM 获取新增的可交互元素（在框架渲染后）
const rescanDom = () => {
	let addedCount = 0;

	// 扫描主文档（包含 Shadow DOM）
	walkShadowDom(document, (node) => {
		if (!state.visited.has(node)) {
			enqueueNode(node);
			addedCount++;
		}
	});

	// 重新扫描 Shadow Roots
	scanShadowRoots();

	// 重新处理 iframe
	processIframes();

	const queueStats = state.queue.stats();
	bridge.emit("dom_rescan", {
		added: addedCount,
		queue_high: queueStats.high,
		queue_normal: queueStats.normal,
		queue_low: queueStats.low,
		queue_total: queueStats.total
	});
};

// ============ 心跳机制 - 定期上报进度防止超时 ============
const heartbeat = {
	intervalId: null,
	lastHeartbeat: performance.now(),
	intervalMs: 5000, // 每5秒发送一次心跳

	start() {
		if (this.intervalId) return;
		this.intervalId = setInterval(() => {
			this.send();
		}, this.intervalMs);
		this.send(); // 立即发送第一次
	},

	send() {
		this.lastHeartbeat = performance.now();
		const queueStats = state.queue.stats();
		bridge.emit("heartbeat", {
			ts: Date.now(),
			elapsed_ms: Math.round(performance.now() - state.startTs),
			actions: state.actions,
			batches: state.batches,
			queue_total: queueStats.total,
			popup_queue: popupState.popupQueue.length,
			urls_found: state.queuedUrls.size,
			pending_requests: adaptiveDelay.pendingRequests,
			stopped: state.stopped,
			// 用于计算延长时间
			extend_ms: Math.max(3000, queueStats.total * 200 + 2000),
		});
	},

	stop() {
		if (this.intervalId) {
			clearInterval(this.intervalId);
			this.intervalId = null;
		}
	}
};

const batchLoop = async () => {
	// 启动心跳
	heartbeat.start();

	// 等待框架渲染完成
	await waitForFrameworkReady();
	// 重新扫描 DOM
	rescanDom();

	// 输出队列初始状态
	const initialStats = state.queue.stats();
	bridge.emit("queue_initialized", {
		high: initialStats.high,
		normal: initialStats.normal,
		low: initialStats.low,
		total: initialStats.total,
		signatures_count: seenSignatures.size,
	});

	const estimated = Math.min(actionLimit, state.queue.length || actionLimit) * averageSlow() + 2000;
	bridge.emit("budget_proposal", {
		estimated_ms: estimated,
		action_limit: actionLimit,
		queue: state.queue.length,
		slow: averageSlow(),
		extend_ms: estimated,
	});
	while ((state.queue.length || popupState.popupQueue.length) && state.actions < actionLimit && !state.stopped) {
		const batchStart = performance.now();
		let batchActions = 0;
		let batchErrors = 0;

		// 优先处理弹窗队列中的元素
		while (popupState.popupQueue.length > 0 && state.actions < actionLimit && batchActions < batchSize) {
			const el = popupState.popupQueue.shift();
			if (!el || !document.contains(el) || !isElementVisible(el)) continue;

			try {
				if (isClickable(el)) {
					await clickNodeWithPopupCheck(el, "popup-element");
					batchActions++;
					state.actions++;
				}
			} catch(err) {
				console.log("popup element error", err);
				batchErrors++;
			}
		}

		// 处理普通队列
		while (state.queue.length && state.actions < actionLimit && batchActions < batchSize) {
			const node = state.queue.shift();
			try {
				await processNode(node);
			} catch(err) {
				console.log("auto error", err);
				batchErrors++;
			}
			batchActions++;
			state.actions++;
		}
		state.batches++;
		const remainingEstimate = Math.min(Math.max(0, actionLimit - state.actions), state.queue.length + popupState.popupQueue.length) * averageSlow() + 2000;
		bridge.emit("batch_done", {
			batch: state.batches,
			actions: batchActions,
			errors: batchErrors,
			total_actions: state.actions,
			queue_left: state.queue.length,
			popup_queue_left: popupState.popupQueue.length,
			urls_found: state.queuedUrls.size,
			duration_ms: Math.round(performance.now() - batchStart),
			extend_ms: remainingEstimate,
		});
		const cmd = await bridge.waitCommand(commandTimeout);
		if (cmd && typeof cmd === "object") {
			if (cmd.type === "stop") {
				state.stopped = true;
				bridge.emit("stopped", {reason: cmd.reason || "external"});
				break;
			}
			if (cmd.type === "set_slow" && typeof cmd.slow === "number" && cmd.slow >= 0) {
				slow = cmd.slow;
				randomSlowEnabled = false;
				randomSlowMin = slow;
				randomSlowMax = slow;
				bridge.emit("slow_updated", {slow});
			}
		}
	}
};

return (async () => {
	try {
		await batchLoop();
	} catch(err) {
		bridge.emit("error", {stage: "batch_loop", message: String(err)});
		throw err;
	} finally {
		heartbeat.stop();
		observer.disconnect();
		popupObserver.disconnect && popupObserver.disconnect();
	}
	const urls = Array.from(state.queuedUrls);
	const duration = Math.round(performance.now() - state.startTs);
	bridge.emit("final_result", {
		urls: urls.length,
		duration_ms: duration,
		total_actions: state.actions,
		batches: state.batches,
	});
	return {
		urls,
		logs: state.actionLogs,
		stats: {
			duration_ms: duration,
			total_actions: state.actions,
			batches: state.batches,
		},
	};
})();
}
`

func Auto(page *rod.Page, extend func(time.Duration)) []string {
	info, err := utils.GetPageInfoByPage(page)
	if err != nil {
		return nil
	}

	configBytes, err := buildAutoConfig()
	if err != nil {
		log.Logger.Errorf("build auto config err: %s", err)
		return []string{info.URL}
	}
	content := fmt.Sprintf(AutoJsTemplate, configBytes)

	bridge := newAutoBridge(page, extend)
	defer bridge.Close()

	timeoutBudget := bridge.SuggestTimeout()
	log.Logger.Debugf("run auto js %s timeout=%s", info.URL, timeoutBudget)
	hrefArrays, err := evalAutoWithRetry(page, content, timeoutBudget)

	if err != nil {
		log.Logger.Debugf("Auto run error: %s", err)
		return []string{info.URL}
	}
	resultJSON := hrefArrays.Value
	urlsJSON := resultJSON.Get("urls")
	if urlsJSON.Raw() == nil {
		urlsJSON = resultJSON
	}
	hrefList := make([]string, 0, len(urlsJSON.Arr()))
	for _, url := range urlsJSON.Arr() {
		hrefList = append(hrefList, url.Str())
	}
	if logsJSON := resultJSON.Get("logs"); logsJSON.Raw() != nil {
		for _, entry := range logsJSON.Arr() {
			tag := entry.Get("tag").Str()
			text := entry.Get("text").Str()
			selector := entry.Get("selector").Str()
			reason := entry.Get("reason").Str()
			log.Logger.Debugf("auto click [%s] selector=%s text=%q reason=%s", tag, selector, text, reason)
		}
	}
	stats := bridge.Stats()
	if stats.BridgeEnabled {
		log.Logger.Debugf("auto stats batches=%d actions=%d duration=%dms", stats.Batches, stats.Actions, stats.TotalDurationMs)
	}
	return static.HandlerUrls(hrefList, info.URL)
}

func buildAutoConfig() (string, error) {
	cfg := autoTemplateConfig{
		Username:         conf.GlobalConfig.LoginConf.Username,
		Password:         conf.GlobalConfig.LoginConf.Password,
		Email:            conf.GlobalConfig.LoginConf.Email,
		Phone:            conf.GlobalConfig.LoginConf.Phone,
		Slow:             conf.GlobalConfig.AutoConf.Slow,
		SlowMin:          conf.GlobalConfig.AutoConf.SlowMin,
		SlowMax:          conf.GlobalConfig.AutoConf.SlowMax,
		Highlight:        conf.GlobalConfig.BrowserConf.UnHeadless,
		Filter:           conf.GlobalConfig.AutoConf.Filter,
		ActionLimit:      conf.GlobalConfig.AutoConf.ActionLimit,
		BatchSize:        conf.GlobalConfig.AutoConf.BatchSize,
		CommandTimeoutMs: conf.GlobalConfig.AutoConf.CommandTimeoutMs,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// EstimateAutoDuration 返回 AutoJS 预估执行时长（含缓冲）
func EstimateAutoDuration() time.Duration {
	actions := conf.GlobalConfig.AutoConf.ActionLimit
	if actions <= 0 {
		actions = 200
	}
	slowMs := conf.GlobalConfig.AutoConf.Slow
	if slowMs <= 0 {
		slowMs = 1000
	}
	if conf.GlobalConfig.AutoConf.SlowMin > 0 && conf.GlobalConfig.AutoConf.SlowMax > conf.GlobalConfig.AutoConf.SlowMin {
		slowMs = (conf.GlobalConfig.AutoConf.SlowMin + conf.GlobalConfig.AutoConf.SlowMax) / 2
	} else if conf.GlobalConfig.AutoConf.SlowMin > 0 {
		slowMs = conf.GlobalConfig.AutoConf.SlowMin
	}
	duration := time.Duration(float64(actions)*slowMs) * time.Millisecond
	if duration <= 0 {
		duration = 15 * time.Second
	}
	return duration + 5*time.Second
}

// evalAutoWithRetry 确保自动脚本执行完再结束，处理 Execution context was destroyed 错误
func evalAutoWithRetry(page *rod.Page, script string, timeout time.Duration) (*proto.RuntimeRemoteObject, error) {
	const maxRetry = 2
	const retryDelay = 300 * time.Millisecond
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if tt := time.Duration(conf.GlobalConfig.BrowserConf.TabTimeout) * time.Second; tt > 0 && timeout > tt {
		timeout = tt - time.Second
		if timeout < 5*time.Second {
			timeout = 5 * time.Second
		}
	}
	timeoutPage := page.Timeout(timeout)
	defer timeoutPage.CancelTimeout()

	var res *proto.RuntimeRemoteObject
	var err error
	for attempt := 0; attempt < maxRetry; attempt++ {
		res, err = timeoutPage.Eval(script)
		if err == nil {
			return res, nil
		}
		if !strings.Contains(err.Error(), "Execution context was destroyed") {
			break
		}
		log.Logger.Debugf("auto eval retry(%d/%d) after context destroyed", attempt+1, maxRetry)
		time.Sleep(retryDelay)
	}
	return res, err
}
