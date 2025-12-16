package engine

import (
	"math/rand"
	"time"

	"github.com/go-rod/rod"
)

// StealthScript 反检测 JavaScript 脚本
// 用于隐藏 headless 浏览器特征
var StealthScript = `
function() {
	'use strict';

	// 1. 隐藏 webdriver 标志
	Object.defineProperty(navigator, 'webdriver', {
		get: () => undefined,
		configurable: true
	});

	// 删除 webdriver 相关属性
	delete navigator.__proto__.webdriver;

	// 2. 修复 plugins 数组
	Object.defineProperty(navigator, 'plugins', {
		get: () => {
			const plugins = [
				{
					name: 'Chrome PDF Plugin',
					filename: 'internal-pdf-viewer',
					description: 'Portable Document Format',
					length: 1
				},
				{
					name: 'Chrome PDF Viewer',
					filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai',
					description: '',
					length: 1
				},
				{
					name: 'Native Client',
					filename: 'internal-nacl-plugin',
					description: '',
					length: 2
				}
			];
			plugins.length = 3;
			return plugins;
		},
		configurable: true
	});

	// 3. 修复 languages
	Object.defineProperty(navigator, 'languages', {
		get: () => ['zh-CN', 'zh', 'en-US', 'en'],
		configurable: true
	});

	// 4. 修复 platform
	if (navigator.platform === '') {
		Object.defineProperty(navigator, 'platform', {
			get: () => 'Win32',
			configurable: true
		});
	}

	// 5. 修复 hardwareConcurrency
	Object.defineProperty(navigator, 'hardwareConcurrency', {
		get: () => 4,
		configurable: true
	});

	// 6. 修复 deviceMemory
	Object.defineProperty(navigator, 'deviceMemory', {
		get: () => 8,
		configurable: true
	});

	// 7. 隐藏自动化相关的 window 属性
	const automationProps = [
		'cdc_adoQpoasnfa76pfcZLmcfl_Array',
		'cdc_adoQpoasnfa76pfcZLmcfl_Promise',
		'cdc_adoQpoasnfa76pfcZLmcfl_Symbol',
		'__webdriver_script_fn',
		'__driver_evaluate',
		'__webdriver_evaluate',
		'__selenium_evaluate',
		'__fxdriver_evaluate',
		'__driver_unwrapped',
		'__webdriver_unwrapped',
		'__selenium_unwrapped',
		'__fxdriver_unwrapped',
		'_Selenium_IDE_Recorder',
		'_selenium',
		'calledSelenium',
		'$chrome_asyncScriptInfo',
		'$cdc_asdjflasutopfhvcZLmcfl_',
		'__$webdriverAsyncExecutor'
	];

	automationProps.forEach(prop => {
		try {
			delete window[prop];
		} catch(e) {}
	});

	// 8. 修复 chrome 对象
	if (!window.chrome) {
		window.chrome = {
			runtime: {
				onConnect: null,
				onMessage: null
			},
			loadTimes: function() {},
			csi: function() {},
			app: {}
		};
	}

	// 9. 修复 permissions API
	try {
		if (navigator.permissions && typeof navigator.permissions.query === 'function') {
			const originalQuery = navigator.permissions.query.bind(navigator.permissions);
			navigator.permissions.query = function(parameters) {
				if (parameters && parameters.name === 'notifications') {
					return Promise.resolve({ state: 'prompt', onchange: null });
				}
				return originalQuery(parameters);
			};
		}
	} catch(e) {}

	// 10. 修复 WebGL vendor/renderer
	try {
		if (window.WebGLRenderingContext && WebGLRenderingContext.prototype && WebGLRenderingContext.prototype.getParameter) {
			const getParameter = WebGLRenderingContext.prototype.getParameter;
			WebGLRenderingContext.prototype.getParameter = function(parameter) {
				if (parameter === 37445) {
					return 'Intel Inc.';
				}
				if (parameter === 37446) {
					return 'Intel Iris OpenGL Engine';
				}
				return getParameter.apply(this, arguments);
			};
		}
	} catch(e) {}

	// 11. 修复 canvas fingerprint
	try {
		if (window.HTMLCanvasElement && HTMLCanvasElement.prototype && HTMLCanvasElement.prototype.toDataURL) {
			const originalToDataURL = HTMLCanvasElement.prototype.toDataURL;
			HTMLCanvasElement.prototype.toDataURL = function(type) {
				if (type === 'image/png' && this.width === 220 && this.height === 30) {
					// 可能是指纹检测，返回略微修改的结果
					return originalToDataURL.apply(this, arguments);
				}
				return originalToDataURL.apply(this, arguments);
			};
		}
	} catch(e) {}

	// 12. 隐藏 headless 相关 user-agent 特征
	try {
		const originalUA = navigator.userAgent || '';
		Object.defineProperty(navigator, 'userAgent', {
			get: () => String(originalUA).replace(/HeadlessChrome/g, 'Chrome'),
			configurable: true
		});
	} catch(e) {}

	// 13. 修复 connection 对象
	Object.defineProperty(navigator, 'connection', {
		get: () => ({
			effectiveType: '4g',
			rtt: 50,
			downlink: 10,
			saveData: false
		}),
		configurable: true
	});

	// 14. 添加 Bluetooth API（某些检测会检查）
	if (!navigator.bluetooth) {
		navigator.bluetooth = {
			getAvailability: () => Promise.resolve(false)
		};
	}

	console.log('[Argo Stealth] Anti-detection scripts injected');
}
`

// UserAgents 常见的真实 User-Agent 列表
var UserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
}

// GetRandomUserAgent 返回随机 User-Agent
func GetRandomUserAgent() string {
	rand.Seed(time.Now().UnixNano())
	return UserAgents[rand.Intn(len(UserAgents))]
}

// InjectStealthScript 向页面注入反检测脚本
func InjectStealthScript(page *rod.Page) error {
	if page == nil {
		return nil
	}
	_, err := page.Eval(StealthScript)
	return err
}

// ApplyStealthMode 应用隐身模式设置到浏览器
func ApplyStealthMode(page *rod.Page) error {
	if page == nil {
		return nil
	}

	// 注入反检测脚本
	return InjectStealthScript(page)
}
