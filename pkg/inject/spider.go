package inject

import (
	"argo/pkg/log"

	"github.com/go-rod/rod"
)

// spiderOverlayJS 注入「可视化爬虫」：一只内联 SVG 的蜘蛛叠加层。
//
// 形态按真实蜘蛛：小身体 + 8 条长腿（两段式高拱，膝在上、足尖落地），
// 腿跨度约 2.5 倍体长；行走时两组腿交替摆动 + 身体起伏。
//
// 为什么用 closed shadow root：蜘蛛的 DOM/动画必须对爬取逻辑完全隐形——
//   - document.querySelectorAll（可点元素列举、链接扫描）看不见 shadow 内部
//   - armMutationJS 的 MutationObserver 只观察 light DOM，蜘蛛移动不会
//     让 settleAfterClick 误判「页面有反应」
//   - 页面 HTML 序列化不含 shadow 内容，ParseDom/extract 不会提取到蜘蛛
//
// host 元素本身 pointer-events:none，不拦截真实点击。
//
// API（挂在 window 上，供 auto 交互调用）：
//
//	window.__argoSpider.tap(x, y) -> Promise  爬到目标旁并伸腿点中，完成才 resolve
//	window.__argoSpider.moveTo(x, y, dur)     平移（内部用）
//
// 蜘蛛未注入时调用方直接跳过（tap 不存在 → 交互零额外延迟）。
const spiderOverlayJS = `() => {
	if (window.__argoSpider) { return true; }
	// 单函数 payload（Eval 要求）。init 独立成函数：EvalOnNewDocument 在
	// document 起点执行时 documentElement 可能还没建好，直接 appendChild 会抛错，
	// 没就绪就等 DOMContentLoaded 再初始化
	function init() {
	const host = document.createElement("div");
	host.id = "argo-spider-host";
	// 不拦截任何真实点击/悬停；不用 all:initial——Chrome 会把它展开成
	// 几百条长属性塞进 style，白白放大序列化体积
	host.style.cssText = "position:fixed;left:0;top:0;width:0;height:0;pointer-events:none;z-index:2147483647;";
	let root;
	try {
		root = host.attachShadow({mode: "closed"});
	} catch (e) { return false; }
	document.documentElement.appendChild(host);

	// ---- 蜘蛛形态（SVG）：精致机械蛛——流线装甲双体 + 两段折线腿，
	// 腿展收敛到约 2.5-3 倍体长（旧版 8 倍张开像海星），步态放慢更显从容。
	// 每条腿是两段直线（髋→膝→足）+ 膝关节微光节点；
	// 髋部 transform-origin 供步态摆动与伸腿。
	// legs[i] = {hx,hy 髋, kx,ky 膝, fx,fy 足, phase 步态组}
	const legDefs = [
		// 右侧四条（前→后）：膝高抬、足尖落地
		{hx: 6, hy: -4, kx: 26, ky: -36, fx: 58, fy: -20, phase: 0},
		{hx: 7, hy: -1, kx: 32, ky: -24, fx: 70, fy: 4, phase: 1},
		{hx: 6, hy: 2, kx: 28, ky: -4, fx: 62, fy: 28, phase: 0},
		{hx: 5, hy: 4, kx: 21, ky: 6, fx: 48, fy: 44, phase: 1},
		// 左侧四条（x 镜像，步态组交错——四足步态）
		{hx: -6, hy: -4, kx: -26, ky: -36, fx: -58, fy: -20, phase: 1},
		{hx: -7, hy: -1, kx: -32, ky: -24, fx: -70, fy: 4, phase: 0},
		{hx: -6, hy: 2, kx: -28, ky: -4, fx: -62, fy: 28, phase: 1},
		{hx: -5, hy: 4, kx: -21, ky: 6, fx: -48, fy: 44, phase: 0},
	];

	const style = document.createElement("style");
	style.textContent = [
		":host,*{margin:0;padding:0}",
		".world{position:fixed;left:0;top:0;width:100vw;height:100vh;pointer-events:none;overflow:hidden}",
		".spider{position:absolute;width:0;height:0;will-change:transform;filter:drop-shadow(0 0 5px rgba(56,232,255,.28))}",
		".spider svg{overflow:visible;display:block}",
		".leg{stroke:#57e6ff;stroke-width:1.5;fill:none;stroke-linecap:round;stroke-linejoin:round;opacity:.92}",
		".joint{fill:#0c1420;stroke:#2aa8c8;stroke-width:.8}",
		".foot{fill:#9ff1ff;opacity:.85}",
		// 步态：四足交替（tetrapod），慢速小幅度——比旧版 0.2s 狂摆从容得多
		".legA{animation:swingA .42s cubic-bezier(.45,0,.55,1) infinite alternate}",
		".legB{animation:swingB .42s cubic-bezier(.45,0,.55,1) infinite alternate}",
		"@keyframes swingA{from{transform:rotate(-2.5deg)}to{transform:rotate(3.5deg)}}",
		"@keyframes swingB{from{transform:rotate(3.5deg)}to{transform:rotate(-2.5deg)}}",
		".core{fill:#0b1220;stroke:#57e6ff;stroke-width:1.1}",
		".shell{fill:rgba(12,20,32,.92);stroke:#2aa8c8;stroke-width:1}",
		".panel{fill:none;stroke:rgba(87,230,255,.35);stroke-width:.8}",
		".visor{fill:none;stroke:#eafcff;stroke-width:1.6;stroke-linecap:round;filter:drop-shadow(0 0 3px rgba(154,241,255,.9))}",
		".pulse{fill:#9ff1ff;animation:corepulse 1.6s ease-in-out infinite alternate}",
		"@keyframes corepulse{from{opacity:.45;r:1.8}to{opacity:1;r:2.8}}",
		".ring{fill:none;stroke:rgba(87,230,255,.22);stroke-width:1;animation:ringspin 14s linear infinite;transform-origin:0 0}",
		"@keyframes ringspin{from{transform:rotate(0)}to{transform:rotate(360deg)}}",
		".bob{animation:bob .42s ease-in-out infinite alternate}",
		".lunge{animation:lunge .3s cubic-bezier(.2,.8,.3,1.3)}",
		"@keyframes lunge{0%{transform:translateY(0) scale(1)}40%{transform:translateY(-3px) scale(1.18)}100%{transform:translateY(0) scale(1)}}",
		"@keyframes bob{from{transform:translateY(0)}to{transform:translateY(-1.5px)}}",
		".tapfx{position:absolute;width:34px;height:34px;border:4px solid rgba(140,245,255,1);border-radius:50%;transform:translate(-50%,-50%) scale(.2);opacity:0;pointer-events:none;box-shadow:0 0 20px rgba(87,230,255,.95),inset 0 0 12px rgba(87,230,255,.6)}",
		".tapfx.go{animation:ripple .38s ease-out forwards}",
		"@keyframes ripple{0%{transform:translate(-50%,-50%) scale(.2);opacity:1}100%{transform:translate(-50%,-50%) scale(2.8);opacity:0}}",
		".tapfx2{position:absolute;width:26px;height:26px;border:2px solid rgba(255,255,255,.85);border-radius:50%;transform:translate(-50%,-50%) scale(.2);opacity:0;pointer-events:none}",
		".tapfx2.go{animation:ripple2 .38s .06s ease-out forwards}",
		"@keyframes ripple2{0%{transform:translate(-50%,-50%) scale(.3);opacity:.9}100%{transform:translate(-50%,-50%) scale(1.5);opacity:0}}",
		".tapbox{position:absolute;pointer-events:none;opacity:0;background:rgba(87,230,255,.12);box-shadow:0 0 20px rgba(87,230,255,.45),inset 0 0 14px rgba(87,230,255,.18)}",
		".tapbox.go{animation:boxflash .8s steps(1) forwards}",
		"@keyframes boxflash{0%{opacity:1}25%{opacity:.1}50%{opacity:1}75%{opacity:.1}100%{opacity:0}}",
		// 四角括号：科幻目标指示器风格
		".tapbox i{position:absolute;width:12px;height:12px;border:3px solid #9ff1ff}",
		".tapbox i:nth-child(1){left:-3px;top:-3px;border-right:none;border-bottom:none}",
		".tapbox i:nth-child(2){right:-3px;top:-3px;border-left:none;border-bottom:none}",
		".tapbox i:nth-child(3){left:-3px;bottom:-3px;border-right:none;border-top:none}",
		".tapbox i:nth-child(4){right:-3px;bottom:-3px;border-left:none;border-top:none}",
		".taplabel{position:absolute;transform:translate(-50%,-100%);background:rgba(10,20,34,.95);border:1px solid #57e6ff;color:#eafcff;font:bold 13px/1.7 ui-monospace,Consolas,monospace;padding:2px 10px;border-radius:4px;white-space:nowrap;pointer-events:none;opacity:0;box-shadow:0 0 14px rgba(87,230,255,.7)}",
		".taplabel.go{animation:labelflash .8s steps(1) forwards}",
		"@keyframes labelflash{0%{opacity:1}25%{opacity:.2}50%{opacity:1}75%{opacity:.2}100%{opacity:0}}",
		".spider.tapping .leg{stroke:#c9f7ff;filter:drop-shadow(0 0 5px rgba(87,230,255,.9))}",
		".spider.tapping .joint{fill:#9ff1ff}",
		// 出击触手：身体→目标的直线，先划出再收回
		".strike{stroke:#eaffff;stroke-width:3.5;stroke-linecap:round;filter:drop-shadow(0 0 8px rgba(87,230,255,1));opacity:0}",
		".strike.go{animation:strikein .32s ease-out forwards}",
		"@keyframes strikein{0%{opacity:1;stroke-dashoffset:var(--len)}35%{opacity:1;stroke-dashoffset:0}100%{opacity:0;stroke-dashoffset:0}}",
	].join("");
	root.appendChild(style);

	const world = document.createElement("div");
	world.className = "world";
	root.appendChild(world);

	// SVG：流线装甲身体（前驾驶舱 + 后装甲舱，面板线点缀）+ 8 条两段机械腿
	const NS = "http://www.w3.org/2000/svg";
	const svg = document.createElementNS(NS, "svg");
	svg.innerHTML = [
		'<defs>',
		'<linearGradient id="argoLegGrad" x1="0" y1="0" x2="1" y2="0">',
		'<stop offset="0%" stop-color="#2aa8c8"/><stop offset="100%" stop-color="#9ff1ff"/>',
		'</linearGradient>',
		'</defs>',
		'<circle class="ring" cx="0" cy="0" r="24" stroke-dasharray="4 12"/>',
		'<g class="bob">',
		'<g id="argoLegs"></g>',
		'<line id="argoStrike" class="strike" x1="0" y1="0" x2="0" y2="0"/>',
		'<circle id="argoGrab" class="foot" cx="0" cy="0" r="0"/>',
		// 腹部（后）：流线装甲舱 + 两条面板线
		'<path class="shell" d="M -6 -7 C -18 -10 -31 -6 -33 1 C -31 8 -18 11 -6 7 C -2 5 -2 -5 -6 -7 Z"/>',
		'<path class="panel" d="M -12 -7.6 C -17 -6 -17 6 -12 7.6"/>',
		'<path class="panel" d="M -20 -6.4 C -25 -4 -25 4 -20 6.4"/>',
		// 头胸部（前）：楔形驾驶舱 + 核心脉冲 + 单条扫描目镜（替代旧版两只小眼）
		'<path class="core" d="M -3 -8 C 5 -10 13 -6 14 0 C 13 6 5 9 -3 7 C -6 5 -6 -6 -3 -8 Z"/>',
		'<circle class="pulse" cx="5" cy="0" r="2.2"/>',
		'<path class="visor" d="M 7 -3.2 L 12 -2.6"/>',
		'</g>',
	].join("");

	const legsGroup = svg.querySelector("#argoLegs");
	const legs = [];
	legDefs.forEach(function (def, i) {
		const g = document.createElementNS(NS, "g");
		g.setAttribute("class", def.phase ? "legB" : "legA");
		g.style.transformOrigin = def.hx + "px " + def.hy + "px";
		const path = document.createElementNS(NS, "path");
		path.setAttribute("class", "leg");
		path.setAttribute("stroke", "url(#argoLegGrad)");
		path.setAttribute("d", "M " + def.hx + " " + def.hy + " L " + def.kx + " " + def.ky + " L " + def.fx + " " + def.fy);
		g.appendChild(path);
		// 膝关节节点（机械关节感）
		const joint = document.createElementNS(NS, "circle");
		joint.setAttribute("class", "joint");
		joint.setAttribute("cx", def.kx);
		joint.setAttribute("cy", def.ky);
		joint.setAttribute("r", 1.7);
		g.appendChild(joint);
		// 足尖光点
		const foot = document.createElementNS(NS, "circle");
		foot.setAttribute("class", "foot");
		foot.setAttribute("cx", def.fx);
		foot.setAttribute("cy", def.fy);
		foot.setAttribute("r", 1.1);
		g.appendChild(foot);
		legsGroup.appendChild(g);
		legs.push({group: g, def: def});
	});

	const spider = document.createElement("div");
	spider.className = "spider";
	spider.appendChild(svg);
	world.appendChild(spider);

	const tapfx = document.createElement("div");
	tapfx.className = "tapfx";
	world.appendChild(tapfx);
	const tapfx2 = document.createElement("div");
	tapfx2.className = "tapfx2";
	world.appendChild(tapfx2);
	const tapbox = document.createElement("div");
	tapbox.className = "tapbox";
	tapbox.innerHTML = "<i></i><i></i><i></i><i></i>";
	world.appendChild(tapbox);
	const taplabel = document.createElement("div");
	taplabel.className = "taplabel";
	world.appendChild(taplabel);

	// SVG 需要有尺寸才能渲染：给 0 尺寸 + overflow visible 时 Chrome 会画出来，
	// 但部分版本裁剪——稳妥起见给一个透明承载尺寸（腿展 ±70/±46，留边）
	svg.setAttribute("width", "180");
	svg.setAttribute("height", "130");
	svg.style.marginLeft = "-90px";
	svg.style.marginTop = "-72px";

	const pos = {x: Math.max(60, window.innerWidth * 0.6), y: window.innerHeight * 0.72};
	let walkToken = 0;

	function render() {
		spider.style.transform = "translate(" + pos.x + "px," + pos.y + "px)";
	}
	render();

	function setWalking(on) {
		legs.forEach(function (l) {
			l.group.style.animationPlayState = on ? "running" : "paused";
		});
		svg.querySelector(".bob").style.animationPlayState = on ? "running" : "paused";
	}
	setWalking(false);

	// moveTo：按距离比例限时平移（爬行步态），dur 毫秒，返回 Promise
	function moveTo(x, y, dur) {
		const token = ++walkToken;
		const dx = x - pos.x, dy = y - pos.y;
		const dist = Math.sqrt(dx * dx + dy * dy);
		// 封顶：单次移动最多 ~170ms；近距（<120px）跳过行走直接出手
		dur = Math.min(dur || 170, 170);
		if (dist < 120) { dur = 60; }
		if (dist < 6) { return Promise.resolve(); }
		setWalking(true);
		// 朝向：行进方向在左侧时整只蜘蛛水平翻转（保持头朝前）
		if (Math.abs(dx) > 8) {
			svg.style.transform = dx < 0 ? "scaleX(-1)" : "";
		}
		const startX = pos.x, startY = pos.y;
		const t0 = performance.now();
		return new Promise(function (resolve) {
			(function step(now) {
				if (token !== walkToken) { setWalking(false); resolve(); return; }
				const k = Math.min(1, (now - t0) / dur);
				pos.x = startX + dx * k;
				pos.y = startY + dy * k;
				render();
				if (k < 1) { requestAnimationFrame(step); }
				else { setWalking(false); resolve(); }
			})(t0);
		});
	}

	// tapLeg：离目标最近的腿转向目标伸出 + 身体到目标的出击触手线。
	// 关键：伸腿前摘掉摆动动画类（legA/legB）——CSS 动画的 transform
	// 优先级高于 inline style，不摘的话 rotate 从未生效（触手效果不可见的根因）。
	function tapLeg(x, y) {
		let best = null, bestScore = -1e9;
		const dir = svg.style.transform ? -1 : 1; // 翻转时角度也要镜像
		legs.forEach(function (l) {
			const fx = pos.x + l.def.fx * dir, fy = pos.y + l.def.fy;
			const score = -Math.hypot(x - fx, y - fy);
			if (score > bestScore) { bestScore = score; best = l; }
		});
		if (best) {
			const angle = Math.atan2(y - pos.y, (x - pos.x) * dir) * 180 / Math.PI;
			const native = Math.atan2(best.def.fy - best.def.hy, (best.def.fx - best.def.hx)) * 180 / Math.PI;
			const delta = angle - native;
			best.animClass = best.animClass || best.group.getAttribute("class");
			best.group.setAttribute("class", ""); // 摘动画，让 inline transform 生效
			best.group.style.transition = "transform .15s cubic-bezier(.2,.8,.3,1.2)";
			best.group.style.transform = "rotate(" + delta + "deg)";
			setTimeout(function () {
				if (!best) { return; }
				best.group.style.transition = "transform .3s ease-in";
				best.group.style.transform = "";
				setTimeout(function () {
					if (best && best.animClass) { best.group.setAttribute("class", best.animClass); }
				}, 320);
			}, 260);
		}
		// 出击触手：身体→目标划线（dash 动画），远距离也能看清点到了哪
		const strike = svg.querySelector("#argoStrike");
		const grab = svg.querySelector("#argoGrab");
		window.__argoSpider._els.strike = strike;
		window.__argoSpider._els.grab = grab;
		window.__argoSpider._els.tapbox = tapbox;
		window.__argoSpider._els.taplabel = taplabel;
		if (strike) {
			const dx = (x - pos.x) * dir, dy = y - pos.y;
			strike.setAttribute("x2", dx);
			strike.setAttribute("y2", dy);
			strike.setAttribute("x1", 0);
			strike.setAttribute("y1", 0);
			strike.setAttribute("transform", dir < 0 ? "scale(-1,1)" : "");
			const len = Math.max(1, Math.hypot(dx, dy));
			strike.style.setProperty("--len", len);
			strike.setAttribute("stroke-dasharray", len);
			strike.classList.remove("go");
			void world.offsetWidth;
			strike.classList.add("go");
			if (grab) {
				grab.setAttribute("cx", dx);
				grab.setAttribute("cy", dy);
				grab.setAttribute("transform", dir < 0 ? "scale(-1,1)" : "");
				grab.setAttribute("r", 3.2);
				setTimeout(function () { grab.setAttribute("r", 0); }, 500);
			}
		}
	}

	// tap：爬到目标旁（留一点身位）→ 伸腿点中 → 目标高亮框（四角括号）
	// + 元素标签（tag#id，让"点了哪个元素"一眼可见）+ 双波纹 + 全身发光。
	// w/h/label 均可选。
	function tap(x, y, w, h, label) {
		// 目标在视口内才去（视口外元素滚过去看不见，直接点）
		const inView = x >= 0 && y >= 0 && x <= window.innerWidth && y <= window.innerHeight;
		const done = inView
			? moveTo(x - 55, y - 38, 260).then(function () { tapLeg(x, y); })
			: Promise.resolve();
		return done.then(function () {
			tapfx.style.left = x + "px";
			tapfx.style.top = y + "px";
			tapfx.classList.remove("go");
			void tapfx.offsetWidth; // 重启动画
			tapfx.classList.add("go");
			tapfx2.style.left = x + "px";
			tapfx2.style.top = y + "px";
			tapfx2.classList.remove("go");
			void tapfx2.offsetWidth;
			tapfx2.classList.add("go");
			if (w && h) {
				tapbox.style.left = (x - w / 2 - 6) + "px";
				tapbox.style.top = (y - h / 2 - 6) + "px";
				tapbox.style.width = (w + 12) + "px";
				tapbox.style.height = (h + 12) + "px";
				tapbox.classList.remove("go");
				void tapbox.offsetWidth;
				tapbox.classList.add("go");
			}
			if (label) {
				taplabel.textContent = label;
				taplabel.style.left = x + "px";
				taplabel.style.top = (y - (h || 20) / 2 - 10) + "px";
				taplabel.classList.remove("go");
				void taplabel.offsetWidth;
				taplabel.classList.add("go");
			}
			spider.classList.add("tapping");
			svg.querySelector(".bob").classList.remove("lunge");
			void world.offsetWidth;
			svg.querySelector(".bob").classList.add("lunge");
			setTimeout(function () { spider.classList.remove("tapping"); }, 400);
		});
	}

	// idle 游走：无交互时随机 waypoint 慢爬，被打断立即让位
	(function idleWander() {
		setTimeout(function () {
			if (walkToken === 0 || spider.classList.contains("walking") === false) {
				const x = 70 + Math.random() * Math.max(80, window.innerWidth - 160);
				const y = 70 + Math.random() * Math.max(80, window.innerHeight - 180);
				moveTo(x, y, 900).then(idleWander);
			} else { idleWander(); }
		}, 1300);
	})();

	window.__argoSpider = {tap: tap, moveTo: moveTo, _pos: pos, _els: {strike: null, grab: null, tapbox: null, taplabel: null}};
	}
	if (!document.documentElement || !document.body) {
		document.addEventListener("DOMContentLoaded", init);
		return "deferred";
	}
	init();
	return true;
}`

// InjectSpiderOverlay 在页面上注入蜘蛛叠加层（有头模式调用）。
// 注入失败只记日志——蜘蛛是纯视觉层，失败不影响任何爬取逻辑。
func InjectSpiderOverlay(page *rod.Page) {
	if !spiderOverlayEnabled() {
		return
	}
	res, err := page.Eval(spiderOverlayJS)
	if err != nil {
		log.Logger.Debugf("inject spider overlay err: %s", err)
		return
	}
	if res != nil {
		log.Logger.Debugf("spider overlay: %v", res.Value.Bool())
	}
}

func spiderOverlayEnabled() bool {
	return confSpiderEnabled()
}

// SpiderOverlayJS 返回蜘蛛叠加层脚本（engine 在页面创建时 EvalOnNewDocument
// 注入，每次导航首帧生效）；未启用时返回空串。
func SpiderOverlayJS() string {
	if !spiderOverlayEnabled() {
		return ""
	}
	return spiderOverlayJS
}

// SpiderOverlayScript 蜘蛛叠加层的脚本源码格式（EvalOnNewDocument 用，IIFE——
// 见 ListenerHookScript 的注释：rod 把该内容当语句执行，函数表达式会被丢弃）。
func SpiderOverlayScript() string {
	if !spiderOverlayEnabled() {
		return ""
	}
	return "(" + spiderOverlayJS + ")();"
}
