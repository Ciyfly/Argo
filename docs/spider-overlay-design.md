# 爬虫可视化：蜘蛛动画叠加层（spider overlay）设计方案

## 背景与目标

参考推特上的纯 JS+CSS 蜘蛛爬行动画（x.com/rybinfx/status/2105700296760688790，未直接抓取，
同类实现见 codepen.io spider crawl / dev.to Halloween overlay）。

给 Argo 加「看得见的爬虫」：开启浏览器界面（--uh / --waitlogin / --dev）时，
页面上有一只蜘蛛在爬；**每次 Argo 点击/选择元素，蜘蛛先爬到该元素旁，伸腿点中它**——
把 auto 交互过程可视化，无头模式零开销。

## 设计

### 1. 蜘蛛本体（纯 JS+CSS，无外部资源）

- 结构：头胸部 + 腹部两个椭圆（CSS border-radius），8 条腿（细长 div，两段式折角），
  深色配色，整体 ~48px。
- 动画：行走时腿交替摆动（CSS keyframes，步态循环）+ 身体轻微起伏；
  idle 时随机游走（在视口内取随机 waypoint 慢爬）。
- 触手点击：指定目标点后，最靠近目标的一条腿旋转伸出（CSS transition ~180ms），
  腿尖到达目标中心，随即执行真实点击。

### 2. 与 auto 交互联动（核心）

- `clickBySigJS` 改为 async：定位元素 → **先 scrollIntoView**（视口外元素先 tap 后滚会点错位置）→
  `getBoundingClientRect` 取中心 → `window.__argoSpider.tap(x, y)`（Promise，蜘蛛爬过去+伸腿完成才 resolve）→ 执行真实 click。
- `dispatchEventsJS`：select 逐项派发前 tap；oncontextmenu/ondblclick 元素（真正挂处理器的）逐个走位 tap。
- `submitFormsJS` async 化：提交前蜘蛛走位到表单中心。
- 蜘蛛不存在（无头未注入）时全部跳过，交互零额外延迟（headless 回归 0.940~0.946 波动内）。

### 3. 不污染爬取（关键工程点，全部用 closed shadow root 解决）

叠加层挂在 `document.documentElement` 上的一个 host 元素里，用 **closed shadow root** 承载：

| 风险 | 对策 |
| --- | --- |
| 蜘蛛元素被 `document.querySelectorAll(autoClickableSelector)` 列为可点元素 | closed shadow 内部对 light-DOM 查询不可见 ✓ |
| 蜘蛛链接/属性被 scanLinksJS / scanLinksDeepJS 收集 | 同上，shadow 内不可见 ✓ |
| 蜘蛛移动触发 armMutationJS 的 MutationObserver，让 settleAfterClick 误判「页面有反应」 | MutationObserver 只观察 light DOM，shadow 内部变化不冒泡进 observer ✓ |
| 蜘蛛挡住/吞掉真实点击 | host 元素 `pointer-events: none` ✓ |
| 蜘蛛 DOM 进入页面 HTML 被 ParseDom/extract 提取 | host 是空壳元素（无 href/文本），页面 HTML 序列化不含 shadow 内容 ✓ |

### 4. 注入时机与开关

- 有头模式（UnHeadless || Dev || WaitLogin）时在 NewTab 页面加载后注入；
  无头不注入。
- config `auto.spider: true`（默认开）可关；有头才有意义。

## 影响范围

- 新增 `pkg/inject/spider.go`（overlay JS：DOM 构建 + CSS + 行走/tap API，~200 行 JS）
- `pkg/inject/auto.go`（clickBySigJS 转 async + tap 联动；dispatchEventsJS select 联动）
- `pkg/engine/tab.go`（有头时注入 overlay）
- `pkg/conf/conf.go`（AutoConf.Spider）

## 风险

1. **tap 等待拖慢交互**：仅影响有头（人工观察场景），单次 tap 上限 ~600ms；
   walk 距离按比例封顶。
2. **闭合 shadow 兼容性**：主流 Chromium 均支持；attachShadow 失败时静默禁用（无蜘蛛不影响爬取）。
3. **视觉遮挡**：半透明 + pointer-events none，短暂遮挡可接受（动画本来就是给人看的）。

## 子任务

- [x] spider overlay JS（closed shadow + 行走/游走/tap 动画 + 波纹特效）
- [x] clickBySigJS async 化 + tap 联动（蜘蛛不存在时零开销）
- [x] select 派发联动（dispatchEventsJS 逐项 tap）
- [x] 有头注入 + auto.spider/--spider 开关（默认开）
- [x] 隔离性验证 TestSpiderOverlayIsolation（三断言全过；另实测 page.HTML() 不含蜘蛛内容）
- [x] classic 回归 0.946 无退步；截图已产出（视觉效果待用户有头模式确认）

## 迭代补充（2026-10-03 用户反馈）

- **画面粘性跟随**：MJPEG 流改为粘住当前页面 ≥4s（`streamStickyHold`），修复并发 tab 翻转导致
  蜘蛛时隐时现（实测 61/61 帧含蜘蛛，均值霓虹像素 82-330，修复前 56-58 且有空洞帧）。
- **任务结束感知**：前端状态徽章（运行中/已完成/已停止）+ 结束 toast 提示，轮询总是刷新
  项目列表（原来只在 live 时刷新，结束时侧栏状态不更新）。E2E：TestConsoleUICompletion。
- **点击/选中特效增强**：
  - 目标高亮框改为科幻四角括号 + 半透明填充，停留 0.9s
  - **元素标签**：框上方显示 `TAG#id "文字"`（SELECT 显示选项、FORM 显示 action）——
    "选中了什么元素"一眼可见
  - 双波纹 + 蜘蛛全身发光 0.5s
  - 前端 E2E（ARGO_WEB_E2E=1 门控，默认跳过保持单测零外部依赖）

## 迭代补充二（2026-10-03 反馈：动画拖慢爬取 + 节奏看不清）

- **动画与交互解耦（关键）**：所有 tap 改 fire-and-forget——点击立即执行，
  蜘蛛异步追着表演。此前 await 版本每次交互阻塞 ~1s（walk 420ms + 稳定 120ms +
  strike 500ms），整站爬取被显著拖慢。实测 10 次连续 tap 调用共 2.4ms（236µs/次）。
- **节奏提速**（7fps MJPEG 下必须短促爆发）：walk 上限 550→260ms；腿摆 0.34→0.2s；
  出击线 0.5→0.32s 加粗到 3.5px；波纹 0.55→0.38s 加大到 34px/4px 边框。
- **双闪设计**：高亮框与元素标签改 steps(1) 双闪（亮-暗-亮-暗，0.8s）——
  7fps 采样下至少有一帧落在亮态，单次淡出以前经常整段被跳过。
- **身体前扑**（lunge）：tap 时蜘蛛整体 scale 1.18 弹性前冲，动作感更强。
- **idle 游走提速**：900ms 行走 / 1.3s 间隔（原 1600/2600），画面更活。
- 参考调研：无现成「蜘蛛点选元素」组件；借鉴光标跟随类库（tgomilar/mouse-animations、
  GSAP cursor 配方）三原则：弹簧缓动、高对比短促爆发、动画不阻塞输入。

## 迭代补充三（2026-10-03 反馈：帧率/卡顿）

- **流媒体重做**：初版轮询 140ms/帧（~7fps）确实卡。先按正统方案试了 CDP
  `Page.startScreencast`（合成器主动推帧）——本环境 chromium 113 两种 headless
  均零帧（探针实测），放弃；改为**自适应轮询广播**：
  - JPEG q60（实测 38-60ms/帧）+ 75ms 目标节拍 + 捕获耗时自适应补睡 → **实测 12.4-13.2fps**（~7fps 的 1.8 倍）
  - 单截图循环 + channel 广播多客户端，慢客户端丢帧不拖慢截图
  - 页面代数（gen）机制：切页时旧循环自杀，无泄漏
- 带宽 ~150-220KB/s（q60 JPEG，比 q75 全尺寸更小）。

## 迭代补充五（2026-10-03：tab 类元素只点一个——两遍制调度）

用户观察：SPA 10 个 tab 只点第一个。根因：单循环深度优先 + tab 每次点击都触发
DOM 变化（changed=true 永不提前退），第一个 tab 吃满 maxRepeat 轮才轮到第二个，
预算耗尽其余 tab 从未被点。改为两遍制：

1. **第一遍（广度）**：每个可点元素保证点一次——所有 tab/菜单挨个轮到；
2. **第二遍（择深）**：只对第一遍「有反应」（DOM 变化或出新链）的元素重复推进，
   连续两轮无新链即收，剩余预算 <30% 不再开新链。

实测：spa 0.736→0.793（新高）；classic 0.946 无回归。
