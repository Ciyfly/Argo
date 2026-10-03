package inject

// stealthScriptJS 反爬伪装（EvalOnNewDocument，IIFE）。
//
// 参照 crawlergo TabInitJS 的伪装面（katana 有更完整的 stealth 包，
// 这里取性价比最高的子集）：
//   - navigator.webdriver = false（最常被检测的一项）
//   - navigator.plugins 非空
//   - window.chrome 对象存在
//   - permissions.query 行为正常化
//   - hardwareConcurrency / deviceMemory 合理化（无头默认值常为 1/0，是明显指纹）
const stealthScriptJS = `(() => {
	if (window.__argoStealth) { return; }
	window.__argoStealth = true;
	try { Object.defineProperty(navigator, "webdriver", {get: () => false}); } catch (e) {}
	try {
		Object.defineProperty(navigator, "plugins", {
			get: () => [
				{name: "Chrome PDF Viewer", filename: "internal-pdf-viewer"},
				{name: "Chromium PDF Viewer", filename: "internal-pdf-viewer"},
				{name: "Microsoft Edge PDF Viewer", filename: "internal-pdf-viewer"},
				{name: "WebKit built-in PDF", filename: "internal-pdf-viewer"},
			],
		});
	} catch (e) {}
	try { window.chrome = window.chrome || {runtime: {}}; } catch (e) {}
	try {
		const originQuery = window.navigator.permissions.query.bind(window.navigator.permissions);
		window.navigator.permissions.query = function (parameters) {
			return parameters.name === "notifications"
				? Promise.resolve({state: Notification.permission})
				: originQuery(parameters);
		};
	} catch (e) {}
	try { Object.defineProperty(navigator, "hardwareConcurrency", {get: () => 8}); } catch (e) {}
	try { Object.defineProperty(navigator, "deviceMemory", {get: () => 8}); } catch (e) {}
})();`

// StealthScript 导出给 engine 注入（IIFE 脚本源码格式，两条通道通用）。
func StealthScript() string { return stealthScriptJS }
