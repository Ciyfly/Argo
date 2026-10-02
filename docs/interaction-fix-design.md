# 交互触发修复与预算优化（classic 轨超越 katana）设计方案

## 背景与目标

基线（docs/crawl-range-baseline.md，2026-10-02 干净测量）：Argo classic **0.785 (117/149)**，katana 0.899。
剩余 23 条缺口全部在 L3 交互。隔离实验（仅首页、tabtimeout 90s）+ 逐 URL debug 追踪后，
缺口归因不是"缺交互类型"——dispatchEventsJS 已覆盖右键/双击/select/快捷键——而是四个**通用 bug**：

| # | 问题 | 证据 | 影响缺口 |
| --- | --- | --- | --- |
| 1 | SVG 等非 HTMLElement 可点元素 `el.click()` 不存在（click 是 HTMLElement 方法），异常被 catch 静默吞掉 | rod 探针实测 `svg.click is not a function` | svg-hotspot |
| 2 | select 逐项派发 change 后才统一收集；而站点惯用法是 change 时清空容器再放链接，DOM 只剩最后一项 | 最小复现：3 个 option 只收到最后 1 个 | select-region-cn/us（eu 偶然存活） |
| 3 | checkbox/radio 未勾选就点依赖按钮（autoFillJS 只填文本类，勾选逻辑在 submitFormsJS 里，时序太晚） | checkbox-bundle 在预算充足的隔离 run 中仍缺 | checkbox-bundle |
| 4 | 队列尾部被浏览器总超时截断：交互产生的链接确实收集到了（debug 实证 `auto js push: 84/84` 含 cart-view/menu-l2 等），但排在 UrlsQueue 尾部，278 个 tab 没跑完就被 browsertimeout 杀掉；其中大量 tab 是访问 `/api/*` 这类无后缀接口——JSON 接口开浏览器纯属浪费 | v3 run 278 tabs/140s 被截断；menu-l2-ops 有 HandlerUrl 记录、零 normalizeStr 记录 | cart×7、menu×3、confirm×2、ctx×2、dblclick×1、shortcut×1 等全部尾部截断项 |

修复 1-4 预计可覆盖全部 23 条缺口（117 → ~140，0.94），超过 katana 的 0.899。

## 现状分析（关键代码位置）

- `pkg/inject/auto.go:262` clickBySigJS：`el.click()` + `catch return false`
- `pkg/inject/auto.go:234` dispatchEventsJS：select 循环派发后 `push()` 只收 `http` 开头值，DOM 链接靠 Auto 末尾 collect
- `pkg/inject/auto.go:37` autoFillJS：checkbox/radio 无处理
- `pkg/engine/tab.go` PendUrlWork：所有非静态后缀 URL 一律开 tab
- `pkg/extract/extract.go` IsDocumentURL：文档类后缀判定（js extract 已用）

## 设计

### 修复 1：非 HTMLElement 可点元素的点击兜底（auto.go clickBySigJS）

```js
if (typeof el.click === "function") { el.click(); }
else { el.dispatchEvent(new MouseEvent("click", {bubbles:true, cancelable:true, view:window})); }
```

通用性：SVG 热区图表、自定义元素、MathML 等真实站点常见；不是按靶场文案驱动。

### 修复 2：select 派发逐项收集（auto.go dispatchEventsJS）

每个 option 派发 change 后**立即**收集全文档 `a[href]` 增量（JS 内维护 Set 去重，返回时统一输出绝对地址），
不等 Go 侧轮询。同时把 contextmenu/dblclick 派发后产生的新 `a[href]` 一并收集（它们的 handler 同样可能清空容器）。
返回值仍走 Auto 的 hrefList 合并。

### 修复 3：autoFillJS 勾选 checkbox/radio

```js
else if (t === "checkbox" || t === "radio") {
    if (!node.checked) { node.checked = true; node.dispatchEvent(new Event("input",{bubbles:true})); node.dispatchEvent(new Event("change",{bubbles:true})); }
}
```

放在交互点击**之前**（autoFillJS 本就在 Auto 第 1 步），时序正确。

### 修复 4：非文档 URL 不开 tab——Go 侧内容类型预检（预算优化，主要收益）

原则：**内容驱动**判定，不看路径关键词（不写 `/api/` 特判）。

在 PendUrlWork 中，URL 通过静态过滤后：
- 若 `extract.IsDocumentURL(url)` 为真（.html/.php/.asp/.jsp 等文档后缀）→ 照旧 PushTabQueue 开浏览器；
- 否则 → 用 Go 侧 http client（req 包，复用代理配置）发一次轻量 GET：
  - 响应 `Content-Type` 为 text/html → 是无后缀页面（SPA 路由 `/spa/shop`、`/about` 等）→ PushTabQueue；
  - 其它（json/xml/js/图片…）→ **直接记为结果**（与 js extract 同语义：发现即结果），不开浏览器。

收益估算：v3 run 中 `/api/*` 与 `.json` 等非文档 tab 占比约 1/4~1/3，全部省下后队列尾部不再被截断。

边界处理：
- 预检请求失败（超时/连接拒绝）→ 保守起见仍开 tab（可能是仅浏览器可达的资源）；
- 预检走代理时与浏览器同代理（复用 req.GetProxyClient）；
- 预检在 PendUrlWork 的 7 个 worker 内联执行（本地靶场 ~10ms，公网站点受超时上限约束，req 包现有 10s 超时）；
- 记录的 PendingUrl 带 Status 与 ResponseHeaders（真实响应），不是凭空捏造。

### 不做的事

- 不引入交互元素文案/站点路径特判（自检：删掉靶场换域名，1-4 修复全部仍成立）
- 不动 maxdepth/预算默认值（bench 公平性）

## 影响范围

- `pkg/inject/auto.go`（clickBySigJS / dispatchEventsJS / autoFillJS 三段 JS 字符串）
- `pkg/engine/tab.go` PendUrlWork（+预检分支）、可能新增 `pkg/engine/precheck.go`
- `pkg/req`：新增返回 Content-Type 的轻量 GET（或复用 GetResponseWithBody）
- 测试：auto_test.go 补 JS 语义断言；预检分支用 httptest 覆盖 html/json/失败三分支

## 风险

1. **预检多一发请求**：对每个无后缀 URL 多一次 Go GET（非浏览器）；被目标限速的风险与浏览器访问同级，可接受；有 --rate 限速器（本次不挂在预检上，预检量小）。
2. **依赖响应内容判定**：服务器对无 Accept 头的 Go 请求返回 404/403 页（text/html）→ 仍开 tab，无害方向。
3. **dispatchEventsJS 变复杂**：JS 内增量收集逻辑用 Set 去重，保持纯函数（无外部状态）。

## 子任务

- [x] 修复 1：click 兜底 dispatchEvent + 单测 (rod 探针证实 svg.click 不存在；TestClickFallsBackToDispatchEventForSvg)
- [x] 修复 2：select 逐项收集 + 单测 (dispatchEventsJS 增加 harvest；TestDispatchEventsHarvestsAfterEachEvent)
- [x] 修复 3：autoFill 勾选 checkbox/radio + 单测 (TestAutoFillChecksCheckboxAndRadio)
- [x] 修复 4：PendUrlWork 内容类型预检 + httptest 单测 (req.ProbeContentType/IsHTMLContentType；html→tab、json→record、失败/无CT→tab)
- [x] 追加修复 5：外层 tab 超时与交互预算两个时钟统一（inject.InteractionBudget 共用，封顶 60s）
- [x] 追加修复 6：固定 tab 超时改为进度驱动 idle 超时（--tabidle，默认 10s；有新链接不杀，硬上限兜底）——参照 katana heuristic「等静默信号不给固定寿命」
- [x] 追加修复 7：HTML 内联 script 完整提取（JS 数组字面量里的路径 DOM 永远不揭示）
- [x] 靶场回归：classic must_rate **0.946 (141/149) > katana 0.899**，且更快（88s vs v6 110s）
- [x] 无回归：spa **0.764**（基线 0.679 提升）；pikachu **67s/165 URL**（原基线 80s/136，更快且多 29 条）
