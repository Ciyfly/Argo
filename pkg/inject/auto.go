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

const state = {
	queue: [],
	queuedUrls: new Set(),
	visited: new WeakSet(),
	actionLogs: [],
	actions: 0,
	batches: 0,
	stopped: false,
	startTs: performance.now(),
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

const shouldSkip = (node) => {
	if (!node || !node.tagName) return true;
	if (state.visited.has(node)) return true;
	return skipTags.indexOf(node.tagName) >= 0;
};

const enqueueNode = (node) => {
	if (shouldSkip(node)) return;
	state.visited.add(node);
	state.queue.push(node);
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

const isClickable = (node) => {
	if (!node) return false;
	const tag = node.tagName;
	if (tag === "BUTTON") return true;
	if (tag === "A") return true;
	if (tag === "INPUT" && ["button","submit"].includes(node.type)) return true;
	if (node.getAttribute("role") === "button") return true;
	if (node.onclick || node.getAttribute("ng-click")) return true;
	if (node.tabIndex >= 0 && hasPointerCursor(node)) return true;
	if (hasPointerCursor(node)) return true;
	if (hasDataClickAttr(node)) return true;
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
		await clickNode(node, "auto-click");
	}
};

const observer = new MutationObserver((mutations) => {
	mutations.forEach((mutation) => {
		mutation.addedNodes && mutation.addedNodes.forEach(enqueueNode);
	});
});
observer.observe(document, {subtree:true, childList:true});

const walker = document.createTreeWalker(document, NodeFilter.SHOW_ELEMENT, null);
while (walker.nextNode()) {
	enqueueNode(walker.currentNode);
}

const popupKeywords = ["modal","popup","dialog","captcha","mask","overlay","float"];
const closeSelectors = [
	"[aria-label*=close]",
	"[data-testid*=close]",
	"[class*=close]",
	".btn-close",
	".close-btn",
	".close-button",
	".icon-close",
	".modal-close",
	".popup-close"
];

const looksLikePopup = (node) => {
	if (!node || node === document.body) return false;
	if (node.tagName === "SCRIPT" || node.tagName === "STYLE") return false;
	try {
		const style = window.getComputedStyle(node);
		if (!style) return false;
		const position = style.position || "";
		if (!["fixed","absolute","sticky"].includes(position)) return false;
		const rect = node.getBoundingClientRect();
		if (!rect || rect.width === 0 || rect.height === 0) return false;
		const area = rect.width * rect.height;
		const viewportArea = window.innerWidth * window.innerHeight;
		const coversScreen = viewportArea > 0 && (area / viewportArea) > 0.2;
		const text = (node.innerText || "").toLowerCase();
		const attrs = (node.className || "") + " " + (node.id || "");
		const hasKeyword = popupKeywords.some(k => attrs.toLowerCase().includes(k) || text.includes(k));
		return coversScreen || hasKeyword || node.getAttribute("role") === "dialog";
	} catch(err) {
		return false;
	}
};

const closePopups = () => {
	const candidates = Array.from(document.querySelectorAll("body *")).filter(looksLikePopup);
	candidates.forEach(node => {
		let closed = false;
		for (const selector of closeSelectors) {
			const btn = node.querySelector(selector);
			if (btn) {
				btn.dispatchEvent(new MouseEvent("click", {bubbles:true}));
				closed = true;
				break;
			}
		}
		if (!closed) {
			node.style.setProperty("display", "none", "important");
			node.style.setProperty("visibility", "hidden", "important");
			closed = true;
		}
		if (closed) {
			state.actionLogs.push({
				tag: "POPUP",
				selector: buildSelector(node),
				text: getNodeText(node),
				reason: "auto-close-popup",
			});
		}
	});
};

const popupObserver = new MutationObserver(closePopups);
popupObserver.observe(document.body || document.documentElement, {subtree:true, childList:true});
closePopups();

const batchLoop = async () => {
	const estimated = Math.min(actionLimit, state.queue.length || actionLimit) * averageSlow() + 2000;
	bridge.emit("budget_proposal", {
		estimated_ms: estimated,
		action_limit: actionLimit,
		queue: state.queue.length,
		slow: averageSlow(),
		extend_ms: estimated,
	});
	while (state.queue.length && state.actions < actionLimit && !state.stopped) {
		const batchStart = performance.now();
		let batchActions = 0;
		let batchErrors = 0;
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
		const remainingEstimate = Math.min(Math.max(0, actionLimit - state.actions), state.queue.length) * averageSlow() + 2000;
		bridge.emit("batch_done", {
			batch: state.batches,
			actions: batchActions,
			errors: batchErrors,
			total_actions: state.actions,
			queue_left: state.queue.length,
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
