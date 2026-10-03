# 注入 JS 设计对比：Argo vs crawlergo vs katana（源码级）

对照样本：crawlergo `pkg/js/javascript.go`（476 行单文件）、katana
`pkg/engine/headless/js/{page-init.js, utils.js}`（534 行，embed + EvalOnNewDocument）、
Argo `pkg/inject/`（auto.go JS 载荷族 + listener_hook.go + spider.go）。
rad 无页面 JS 注入层（纯 rod 驱动 + 配置），不在 JS 对比范围。

## 三家的整体架构

### crawlergo：单文件大脚本 + 属性标记 + 双触发器
```
TabInitJS（一段 IIFE 全做）
├── 反爬伪装（webdriver/plugins/chrome 对象）
├── addEventListener 劫持 → setAttribute("sec_auto_dom2_event_flag") 标记
├── DOM0：Object.defineProperties 劫持 20 个 onxxx setter → 同上标记
├── 路由/API 捕获：pushState/replaceState/hashchange/fetch/XHR/WebSocket/EventSource/open
├── 防护：form.reset 锁 / window.close 锁 / setInterval→60s / XHR 同请求限 10 次
└── ObserverJS：DOMNodeInserted 等废弃事件监听新增节点 → 即时收链
触发：TriggerDom2EventJS（按标记逐个 dispatchEvent CustomEvent + transmit_child
      向子节点递归广播，处理事件委托）+ TriggerInlineEventJS（按 onxxx 属性逐个触发，
      每类封顶 100）+ TriggerJavascriptProtocol（eval javascript: href）
执行方式：全部 JS 合成事件（dispatchEvent），无真实鼠标
```

### katana：双文件分工 + 元素快照回传 + 真实鼠标
```
page-init.js（IIFE，EvalOnNewDocument，statement 级 hook）
├── __navigatedLinks：pushState/replaceState/open/WebSocket/EventSource/fetch 捕获
│   （defineProperty value+non-writable，防页面反改）
├── __eventListeners：addEventListener 劫持——但记录的是「元素快照」
│   （tag/id/class/xpath/cssSelector/outerHTML 前 100 字/textContent/hidden…），
│   不持有元素引用，经 Go 侧反查（getElementFromXPath）
├── setTimeout/setInterval 加速 0.1×（把延时动作催熟）——与 crawlergo 的降频相反
└── 防护同款（form.reset 可配 / close 锁）
utils.js：纯工具函数库（getXPath/getCssPath/元素序列化）
执行：element.Click = Hover + WaitEnabled + CDP 真实鼠标（Input.dispatchMouseEvent），
      交互前检查 visible + Interactable（是否被遮挡）
调度：事件监听器快照 → Go 侧按 relevantEventListeners 白名单筛选 → 动作队列
```

### Argo（我们）
```
listener_hook.go（IIFE 脚本，EvalOnNewDocument + 当前文档重试兜底）
├── __argoLsn：addEventListener + DOM0 setter（6 个交互事件）→ JS 属性标记
│   （不碰 DOM 属性：不触发自身 MutationObserver、不进 HTML 序列化）
├── __argoRoutes：pushState/replaceState/hashchange/popstate
├── 防护：close 锁 / setInterval→60s / XHR 限 10 次
auto.go 载荷族（每个动作一个短 JS，循环/调度/超时全部 Go 侧）
├── 收集器 argoClickableNodes：结构选择器 + __argoLsn 标记 + cursor:pointer（封顶）
│   + 同源 iframe 穿透（f<idx> 前缀 sig）
├── clickBySigJS：签名定位 → scrollIntoView → 真实 click()/MouseEvent 兜底
├── 两遍制调度（广度保底 → 择深推进）→ 真实鼠标 hover（CDP Input，后置+预算守卫）
├── dispatchEventsJS：select 逐项/contextmenu/dblclick/快捷键
└── 网络层全量捕获走 HijackRequests（Go 侧，非 JS hook）——不依赖 fetch/XHR 劫持
spider.go：closed shadow 动画层（纯可视化，与爬取解耦）
```

## 设计哲学的三点本质差异

### 1. 「在哪一层闭环」不同
- **crawlergo**：尽量在 JS 里闭环（标记、触发、收集都在页面里），Go 只做编排。
  优点是原子性好；缺点是 JS 执行上下文随导航即灭，长流程必须依赖 deprecated DOM 事件。
- **katana**：JS 只做「观察者」（hook 捕获 + 快照序列化），决策与执行全部在 Go。
  元素用 xpath 反查，天然抗 DOM 变动。
- **我们**：与 katana 同层（JS 单步动作、Go 驱动循环），历史原因——早期把整套
  流程写进一个 JS 因导航崩上下文吃过亏（auto.go 注释留档）。签名定位比 xpath 轻，
  但 DOM 大改时签名漂移的风险比 xpath 高（文本归一化缓解）。

### 2. 「如何发现可点元素」不同
- crawlergo：被动等待标记（监听劫持）+ onxxx 属性选择器。
- katana：监听劫持快照 + Go 侧白名单过滤（relevantEventListeners 21 种事件）。
- 我们：三路收集器（被动标记 + 结构 + **cursor:pointer 计算样式**）——
  第三路是两家都没有的兜底，代价是噪声需要封顶与死元素快速跳过配合。

### 3. 「点击的物理层」不同
- crawlergo：dispatchEvent 合成事件（够触发监听器，触发不了 CSS :hover）。
- katana：element.Click = Hover + CDP 真鼠标——最接近人，还能发现「被遮挡」。
- 我们：JS click() 为主（快、可批量）+ hover 阶段补 CDP 真鼠标。
  取舍：真鼠标每步都要坐标换算+滚动+可交互检查，全量真鼠标会拖慢节奏，
  所以只在 hover 语义必需处使用。

## 各家独有的巧思（值得记取）

| 来源 | 设计 | 我们的状态 |
| --- | --- | --- |
| katana | **元素快照而非引用**（xpath/cssSelector 序列化回传，Go 反查） | 签名方案等价轻量版；xpath 反查可作为签名失效时的兜底 |
| katana | **定时器加速 0.1×**（催熟延时动作，如 3s 后的 XHR） | 未做——与 crawlergo 的降频哲学相反：加速利于发现延时入口，降频利于减噪。可按场景配 |
| katana | **Interactable 检查**（元素被弹窗/遮挡时跳过） | 未做——我们的 click() 对遮挡元素仍会触发监听器，影响不大但真实度低 |
| crawlergo | **transmit_child**（向子节点递归广播事件，处理父级监听的事件委托） | el.click() 天然冒泡覆盖 click 委托；非 click 委托场景仍有差距 |
| crawlergo | 反爬伪装 | 未做（backlog） |
| 我们 | 网络层 HijackRequests 全量捕获 | 两家都没有的更全路径（JS hook 漏 beacon/img/静态请求） |
| 我们 | JS 属性标记（不触发自身观察器） | crawlergo 用 setAttribute 需临时摘 DOM 监听规避反馈环 |
| 我们 | 进度驱动 idle 超时 + 两遍制调度 + 动态预算 | 三家中调度最自适应 |
| 我们 | iframe 穿透点击 | crawlergo 用隐藏 iframe 承接 form target（不同问题），katana 支持子 frame 导航；直接点击同源 iframe 内元素我们已做 |
| 我们 | closed shadow 可视化层 | 独有 |

## 结论

三家是三条路线：crawlergo「页面内闭环」（重 JS hook、合成事件）、
katana「JS 观察 + Go 决策 + 真实鼠标」（工程最正）、我们「JS 单步 + Go 驱动 +
网络层捕获」（覆盖面最广）。我们已吸收两家的关键钩子面（监听/路由/防护/iframe/
真鼠标/静态提取），剩余差异主要是取舍而非能力缺失：反爬伪装、定时器加速、
Interactable 检查、xpath 兜底定位四个小项。
