package engine

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"argo/pkg/log"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// EnhancedStealth 增强反检测模块
type EnhancedStealth struct {
	mu              sync.RWMutex
	fingerprints    []*BrowserFingerprint
	currentFP       *BrowserFingerprint
	rotationEnabled bool
	rotationCount   int
	maxRotation     int
}

// BrowserFingerprint 浏览器指纹配置
type BrowserFingerprint struct {
	UserAgent           string
	Platform            string
	Languages           []string
	Vendor              string
	HardwareConcurrency int
	DeviceMemory        int
	ScreenWidth         int
	ScreenHeight        int
	ColorDepth          int
	PixelRatio          float64
	Timezone            string
	WebGLVendor         string
	WebGLRenderer       string
	AudioContext        bool
}

// DefaultFingerprints 预定义的真实浏览器指纹
var DefaultFingerprints = []*BrowserFingerprint{
	// Windows Chrome
	{
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
		Platform:            "Win32",
		Languages:           []string{"zh-CN", "zh", "en-US", "en"},
		Vendor:              "Google Inc.",
		HardwareConcurrency: 8,
		DeviceMemory:        8,
		ScreenWidth:         1920,
		ScreenHeight:        1080,
		ColorDepth:          24,
		PixelRatio:          1.0,
		Timezone:            "Asia/Shanghai",
		WebGLVendor:         "Google Inc. (NVIDIA)",
		WebGLRenderer:       "ANGLE (NVIDIA, NVIDIA GeForce GTX 1060 Direct3D11 vs_5_0 ps_5_0, D3D11)",
		AudioContext:        true,
	},
	// Windows Chrome 2
	{
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		Platform:            "Win32",
		Languages:           []string{"en-US", "en"},
		Vendor:              "Google Inc.",
		HardwareConcurrency: 4,
		DeviceMemory:        16,
		ScreenWidth:         2560,
		ScreenHeight:        1440,
		ColorDepth:          24,
		PixelRatio:          1.0,
		Timezone:            "America/New_York",
		WebGLVendor:         "Google Inc. (Intel)",
		WebGLRenderer:       "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)",
		AudioContext:        true,
	},
	// Mac Chrome
	{
		UserAgent:           "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
		Platform:            "MacIntel",
		Languages:           []string{"zh-CN", "zh", "en-US", "en"},
		Vendor:              "Google Inc.",
		HardwareConcurrency: 8,
		DeviceMemory:        8,
		ScreenWidth:         1440,
		ScreenHeight:        900,
		ColorDepth:          30,
		PixelRatio:          2.0,
		Timezone:            "Asia/Shanghai",
		WebGLVendor:         "Apple Inc.",
		WebGLRenderer:       "Apple M1 Pro",
		AudioContext:        true,
	},
	// Windows Firefox
	{
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:122.0) Gecko/20100101 Firefox/122.0",
		Platform:            "Win32",
		Languages:           []string{"zh-CN", "zh", "en-US", "en"},
		Vendor:              "",
		HardwareConcurrency: 8,
		DeviceMemory:        0, // Firefox 不暴露此属性
		ScreenWidth:         1920,
		ScreenHeight:        1080,
		ColorDepth:          24,
		PixelRatio:          1.0,
		Timezone:            "Asia/Shanghai",
		WebGLVendor:         "Intel",
		WebGLRenderer:       "Intel(R) UHD Graphics 630",
		AudioContext:        true,
	},
	// Windows Edge
	{
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36 Edg/121.0.0.0",
		Platform:            "Win32",
		Languages:           []string{"zh-CN", "zh", "en-US", "en"},
		Vendor:              "Google Inc.",
		HardwareConcurrency: 12,
		DeviceMemory:        32,
		ScreenWidth:         3840,
		ScreenHeight:        2160,
		ColorDepth:          24,
		PixelRatio:          1.5,
		Timezone:            "Asia/Shanghai",
		WebGLVendor:         "Google Inc. (AMD)",
		WebGLRenderer:       "ANGLE (AMD, AMD Radeon RX 6800 XT Direct3D11 vs_5_0 ps_5_0, D3D11)",
		AudioContext:        true,
	},
}

// NewEnhancedStealth 创建增强反检测模块
func NewEnhancedStealth() *EnhancedStealth {
	es := &EnhancedStealth{
		fingerprints:    DefaultFingerprints,
		rotationEnabled: true,
		maxRotation:     50, // 每50次请求轮换一次指纹
	}
	es.currentFP = es.selectRandomFingerprint()
	return es
}

// selectRandomFingerprint 随机选择指纹
func (es *EnhancedStealth) selectRandomFingerprint() *BrowserFingerprint {
	if len(es.fingerprints) == 0 {
		return DefaultFingerprints[0]
	}
	return es.fingerprints[rand.Intn(len(es.fingerprints))]
}

// GetCurrentFingerprint 获取当前指纹
func (es *EnhancedStealth) GetCurrentFingerprint() *BrowserFingerprint {
	es.mu.RLock()
	defer es.mu.RUnlock()
	return es.currentFP
}

// RotateFingerprint 轮换指纹
func (es *EnhancedStealth) RotateFingerprint() {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.rotationCount++
	if es.rotationEnabled && es.rotationCount >= es.maxRotation {
		es.currentFP = es.selectRandomFingerprint()
		es.rotationCount = 0
		log.Logger.Debugf("Fingerprint rotated to: %s", es.currentFP.UserAgent[:50])
	}
}

// GenerateStealthScript 生成针对当前指纹的反检测脚本
func (es *EnhancedStealth) GenerateStealthScript() string {
	fp := es.GetCurrentFingerprint()
	if fp == nil {
		fp = DefaultFingerprints[0]
	}

	return fmt.Sprintf(`
function() {
	'use strict';

	// ===== 核心反检测 =====

	// 1. 完全移除 webdriver 标志
	Object.defineProperty(navigator, 'webdriver', {
		get: () => undefined,
		configurable: true
	});
	delete navigator.__proto__.webdriver;

	// 2. 伪装 navigator 属性
	const navigatorOverrides = {
		platform: '%s',
		vendor: '%s',
		hardwareConcurrency: %d,
		deviceMemory: %d,
		languages: %s,
		language: '%s'
	};

	for (const [key, value] of Object.entries(navigatorOverrides)) {
		if (value !== null && value !== 0) {
			Object.defineProperty(navigator, key, {
				get: () => value,
				configurable: true
			});
		}
	}

	// 3. 伪装 plugins（Chrome特有）
	const pluginData = [
		{ name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
		{ name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', description: '' },
		{ name: 'Native Client', filename: 'internal-nacl-plugin', description: '' }
	];

	const plugins = {
		length: pluginData.length,
		item: (i) => pluginData[i],
		namedItem: (name) => pluginData.find(p => p.name === name),
		refresh: () => {}
	};
	pluginData.forEach((p, i) => { plugins[i] = p; });

	Object.defineProperty(navigator, 'plugins', {
		get: () => plugins,
		configurable: true
	});

	// 4. 伪装 mimeTypes
	const mimeTypes = {
		length: 2,
		item: (i) => mimeTypes[i],
		namedItem: (name) => null,
		0: { type: 'application/pdf', suffixes: 'pdf', description: 'Portable Document Format' },
		1: { type: 'text/pdf', suffixes: 'pdf', description: 'Portable Document Format' }
	};

	Object.defineProperty(navigator, 'mimeTypes', {
		get: () => mimeTypes,
		configurable: true
	});

	// 5. 伪装屏幕属性
	const screenOverrides = {
		width: %d,
		height: %d,
		availWidth: %d,
		availHeight: %d,
		colorDepth: %d,
		pixelDepth: %d
	};

	for (const [key, value] of Object.entries(screenOverrides)) {
		Object.defineProperty(screen, key, {
			get: () => value,
			configurable: true
		});
	}

	Object.defineProperty(window, 'devicePixelRatio', {
		get: () => %f,
		configurable: true
	});

	// 6. 伪装 WebGL
	const getParameterOriginal = WebGLRenderingContext.prototype.getParameter;
	WebGLRenderingContext.prototype.getParameter = function(param) {
		if (param === 37445) return '%s'; // UNMASKED_VENDOR_WEBGL
		if (param === 37446) return '%s'; // UNMASKED_RENDERER_WEBGL
		return getParameterOriginal.call(this, param);
	};

	// WebGL2 同样处理
	if (typeof WebGL2RenderingContext !== 'undefined') {
		const getParameter2Original = WebGL2RenderingContext.prototype.getParameter;
		WebGL2RenderingContext.prototype.getParameter = function(param) {
			if (param === 37445) return '%s';
			if (param === 37446) return '%s';
			return getParameter2Original.call(this, param);
		};
	}

	// 7. 伪装 Chrome 对象
	if (!window.chrome) {
		window.chrome = {};
	}
	window.chrome.runtime = {
		connect: () => {},
		sendMessage: () => {},
		onConnect: { addListener: () => {} },
		onMessage: { addListener: () => {} }
	};
	window.chrome.loadTimes = function() {
		return {
			commitLoadTime: Date.now() / 1000,
			connectionInfo: 'http/1.1',
			finishDocumentLoadTime: Date.now() / 1000,
			finishLoadTime: Date.now() / 1000,
			firstPaintAfterLoadTime: 0,
			firstPaintTime: Date.now() / 1000,
			navigationType: 'Other',
			npnNegotiatedProtocol: 'http/1.1',
			requestTime: Date.now() / 1000,
			startLoadTime: Date.now() / 1000,
			wasAlternateProtocolAvailable: false,
			wasFetchedViaSpdy: false,
			wasNpnNegotiated: false
		};
	};
	window.chrome.csi = function() {
		return {
			onloadT: Date.now(),
			pageT: Date.now() - performance.timing.navigationStart,
			startE: performance.timing.navigationStart,
			tran: 15
		};
	};

	// 8. 移除自动化标志
	const automationProps = [
		'__webdriver_script_fn', '__driver_evaluate', '__webdriver_evaluate',
		'__selenium_evaluate', '__fxdriver_evaluate', '__driver_unwrapped',
		'__webdriver_unwrapped', '__selenium_unwrapped', '__fxdriver_unwrapped',
		'_Selenium_IDE_Recorder', '_selenium', 'calledSelenium',
		'$chrome_asyncScriptInfo', '$cdc_asdjflasutopfhvcZLmcfl_', '__$webdriverAsyncExecutor',
		'webdriver', 'domAutomation', 'domAutomationController'
	];

	// 使用正则匹配 cdc_ 开头的属性
	for (const key in window) {
		if (key.match(/^cdc_|^__cdc_/)) {
			try { delete window[key]; } catch(e) {}
		}
	}

	automationProps.forEach(prop => {
		try { delete window[prop]; } catch(e) {}
		try { delete document[prop]; } catch(e) {}
	});

	// 9. 伪装 permissions API
	const permissions = navigator.permissions;
	const originalQuery = permissions && typeof permissions.query === 'function'
		? permissions.query.bind(permissions)
		: null;
	if (permissions) {
		permissions.query = function(params) {
			if (params && params.name === 'notifications') {
				return Promise.resolve({ state: 'prompt', onchange: null });
			}
			if (params && params.name === 'push') {
				return Promise.resolve({ state: 'prompt', onchange: null });
			}
			if (params && params.name === 'midi') {
				return Promise.resolve({ state: 'prompt', onchange: null });
			}
			if (originalQuery) {
				return originalQuery(params);
			}
			return Promise.resolve({ state: 'prompt', onchange: null });
		};
	}

	// 10. 伪装 connection API
	Object.defineProperty(navigator, 'connection', {
		get: () => ({
			effectiveType: '4g',
			rtt: 50,
			downlink: 10,
			saveData: false,
			type: 'wifi',
			onchange: null
		}),
		configurable: true
	});

	// 11. 伪装 getBattery
	navigator.getBattery = () => Promise.resolve({
		charging: true,
		chargingTime: 0,
		dischargingTime: Infinity,
		level: 1,
		onchargingchange: null,
		onchargingtimechange: null,
		ondischargingtimechange: null,
		onlevelchange: null
	});

	// 12. 防止 canvas 指纹识别
	const originalGetContext = HTMLCanvasElement.prototype.getContext;
	HTMLCanvasElement.prototype.getContext = function(type, attributes) {
		const ctx = originalGetContext.call(this, type, attributes);
		if (type === '2d' && ctx) {
			const originalGetImageData = ctx.getImageData;
			ctx.getImageData = function(x, y, w, h) {
				const imageData = originalGetImageData.call(this, x, y, w, h);
				// 添加微小噪声
				for (let i = 0; i < imageData.data.length; i += 4) {
					imageData.data[i] ^= 1;     // R
					imageData.data[i+1] ^= 1;   // G
				}
				return imageData;
			};
		}
		return ctx;
	};

	// 13. 伪装 AudioContext 指纹
	if (window.AudioContext || window.webkitAudioContext) {
		const AudioContext = window.AudioContext || window.webkitAudioContext;
		const originalCreateAnalyser = AudioContext.prototype.createAnalyser;
		AudioContext.prototype.createAnalyser = function() {
			const analyser = originalCreateAnalyser.call(this);
			const originalGetFloatFrequencyData = analyser.getFloatFrequencyData;
			analyser.getFloatFrequencyData = function(array) {
				originalGetFloatFrequencyData.call(this, array);
				// 添加微小噪声
				for (let i = 0; i < array.length; i++) {
					array[i] += (Math.random() - 0.5) * 0.0001;
				}
			};
			return analyser;
		};
	}

	// 14. 伪装时区
	const timezone = '%s';
	if (timezone) {
		const DateTimeFormat = Intl.DateTimeFormat;
		Intl.DateTimeFormat = function(locale, options) {
			options = options || {};
			options.timeZone = options.timeZone || timezone;
			return new DateTimeFormat(locale, options);
		};
		Intl.DateTimeFormat.prototype = DateTimeFormat.prototype;

		const resolvedOptions = DateTimeFormat.prototype.resolvedOptions;
		DateTimeFormat.prototype.resolvedOptions = function() {
			const result = resolvedOptions.call(this);
			result.timeZone = timezone;
			return result;
		};
	}

	// 15. 伪装 iframe contentWindow
	const originalContentWindow = Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype, 'contentWindow');
	if (originalContentWindow && originalContentWindow.get) {
		Object.defineProperty(HTMLIFrameElement.prototype, 'contentWindow', {
			get: function() {
				const win = originalContentWindow.get.call(this);
				if (win) {
					try {
						Object.defineProperty(win.navigator, 'webdriver', { get: () => undefined });
					} catch(e) {}
				}
				return win;
			}
		});
	}

	// 16. 防止 toString 检测
	const nativeToString = Function.prototype.toString;
	Function.prototype.toString = function() {
		if (permissions && this === permissions.query) {
			return 'function query() { [native code] }';
		}
		if (this === navigator.getBattery) {
			return 'function getBattery() { [native code] }';
		}
		return nativeToString.call(this);
	};

	console.log('[Argo Enhanced Stealth] Fingerprint protection active');
}
`,
		fp.Platform,
		fp.Vendor,
		fp.HardwareConcurrency,
		fp.DeviceMemory,
		fmt.Sprintf(`["%s"]`, joinStrings(fp.Languages)),
		fp.Languages[0],
		fp.ScreenWidth,
		fp.ScreenHeight,
		fp.ScreenWidth,
		fp.ScreenHeight-40, // availHeight 减去任务栏
		fp.ColorDepth,
		fp.ColorDepth,
		fp.PixelRatio,
		fp.WebGLVendor,
		fp.WebGLRenderer,
		fp.WebGLVendor,
		fp.WebGLRenderer,
		fp.Timezone,
	)
}

// joinStrings 连接字符串数组
func joinStrings(strs []string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += `", "`
		}
		result += s
	}
	return result
}

// ApplyEnhancedStealth 应用增强反检测
func (es *EnhancedStealth) ApplyEnhancedStealth(page *rod.Page) error {
	if page == nil {
		return nil
	}

	// 轮换检查
	es.RotateFingerprint()

	fp := es.GetCurrentFingerprint()

	// 设置 User-Agent
	err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: fp.UserAgent,
	})
	if err != nil {
		log.Logger.Debugf("SetUserAgent error: %v", err)
	}

	// 注入反检测脚本
	script := es.GenerateStealthScript()
	_, err = page.Eval(script)
	if err != nil {
		log.Logger.Debugf("Inject stealth script error: %v", err)
	}

	return nil
}

// AddCustomFingerprint 添加自定义指纹
func (es *EnhancedStealth) AddCustomFingerprint(fp *BrowserFingerprint) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.fingerprints = append(es.fingerprints, fp)
}

// SetRotationEnabled 设置是否启用指纹轮换
func (es *EnhancedStealth) SetRotationEnabled(enabled bool) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.rotationEnabled = enabled
}

// SetMaxRotation 设置轮换阈值
func (es *EnhancedStealth) SetMaxRotation(max int) {
	es.mu.Lock()
	defer es.mu.Unlock()
	if max > 0 {
		es.maxRotation = max
	}
}

// MouseMovementSimulator 鼠标移动模拟器（更真实的人类行为）
type MouseMovementSimulator struct {
	page *rod.Page
}

// NewMouseMovementSimulator 创建鼠标模拟器
func NewMouseMovementSimulator(page *rod.Page) *MouseMovementSimulator {
	return &MouseMovementSimulator{page: page}
}

// SimulateHumanMovement 模拟人类鼠标移动
func (m *MouseMovementSimulator) SimulateHumanMovement(targetX, targetY float64) error {
	if m.page == nil {
		return nil
	}

	// 获取当前位置（默认从中心开始）
	startX := 400.0
	startY := 300.0

	// 使用贝塞尔曲线模拟人类移动
	steps := 20 + rand.Intn(20)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)

		// 三次贝塞尔曲线
		cp1x := startX + (targetX-startX)*0.3 + float64(rand.Intn(50)-25)
		cp1y := startY + (targetY-startY)*0.1 + float64(rand.Intn(50)-25)
		cp2x := startX + (targetX-startX)*0.7 + float64(rand.Intn(30)-15)
		cp2y := startY + (targetY-startY)*0.9 + float64(rand.Intn(30)-15)

		x := bezierPoint(t, startX, cp1x, cp2x, targetX)
		y := bezierPoint(t, startY, cp1y, cp2y, targetY)

		mouse := m.page.Mouse
		err := mouse.MoveLinear(proto.NewPoint(x, y), 1)
		if err != nil {
			return err
		}

		// 随机延迟模拟人类速度变化
		time.Sleep(time.Duration(5+rand.Intn(15)) * time.Millisecond)
	}

	return nil
}

// bezierPoint 计算贝塞尔曲线上的点
func bezierPoint(t, p0, p1, p2, p3 float64) float64 {
	u := 1 - t
	return u*u*u*p0 + 3*u*u*t*p1 + 3*u*t*t*p2 + t*t*t*p3
}

// SimulateTyping 模拟人类打字
func SimulateTyping(page *rod.Page, text string) error {
	if page == nil {
		return nil
	}

	for _, char := range text {
		// 随机延迟模拟打字速度
		delay := 50 + rand.Intn(150)
		time.Sleep(time.Duration(delay) * time.Millisecond)

		// 偶尔出现"犹豫"
		if rand.Float32() < 0.05 {
			time.Sleep(time.Duration(200+rand.Intn(500)) * time.Millisecond)
		}

		err := page.InsertText(string(char))
		if err != nil {
			return err
		}
	}

	return nil
}
