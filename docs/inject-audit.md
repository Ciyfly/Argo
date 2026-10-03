# 注入/点击 JS 设计审计（对照 crawlergo / katana / rad）

审计时间：2026-10-03。crawlergo 源码（/tmp/crawlergo_src/pkg/js/javascript.go 全文）、
katana headless 源码（/tmp/katana_src/pkg/engine/headless）逐项对照。

## 本次审计发现并已修复

1. **EvalOnNewDocument 用法错误（重大，含一次误判更正）**：rod 的该接口把内容当
   **语句**执行，`() => {...}` 传进去只是求值后丢弃、从不执行——此前据此误判
   "环境不支持"（探针也是同格式所以两场景 0）。IIFE 包裹后实测生效。
   现注入为两层：EvalOnNewDocument（后续导航 document 起点）+ 当前文档短重试。
2. **DOM0 监听未劫持**：`el.onclick = fn` 赋值路径捕获不到。已加 onxxx setter
   劫持（click/dblclick/change/submit/mouseover/mousedown，crawlergo 全量 20 个，
   我们收交互相关的 6 个）。
3. **SPA 路由源头未捕获**：pushState/replaceState/hashchange/popstate 现在 hook 进
   `__argoRoutes` 并入链接收集（此前靠事后读 location，跳转链会断）。
4. **页面自杀保护**：window.close 锁定。
5. **定时器降频**：setInterval → 60s（轮询/动画的 DOM 变化噪声会让
   settleAfterClick 的 changed 永真；crawlergo 同款）。
6. **XHR 限次**：同 method+url 最多 10 次（防无限轮询拖忙浏览器；crawlergo 同款）。

实测：spa **0.793 → 0.836**；classic 0.940（波动带内）。

## 我们的领先面（对照三家）

| 能力 | Argo | crawlergo | katana |
|---|---|---|---|
| 网络层全量捕获 | ✅ HijackRequests（含 beacon/img，比 JS 层 fetch/XHR hook 更全） | JS 层 hook | ✅ 类似 |
| Shadow DOM 链接收集 | ✅ 深度遍历 shadow root | ❌ | ❌ |
| 标记不触发自身观察器 | ✅ JS 属性标记（天然避开 MutationObserver 反馈环） | ⚠️ setAttribute 标记，需临时摘除 DOM 监听规避 | — |
| 调度 | 两遍制（广度+择深）+ 进度驱动 idle + 动态预算 | 固定 interval/maxInteractive | 固定 |
| 动画可视化/控制台 | ✅ | ❌ | ❌ |

## 仍未解决的差距（backlog，按价值排序）

1. **iframe 内交互**：点击循环 querySelectorAll 不穿透 iframe（crawlergo 用隐藏
   iframe + form target 承接，katana 支持子 frame 导航）。真实站点 iframe
   表单/菜单会漏。
2. **真实鼠标事件**：我们全部 JS dispatch，触发不了 CSS `:hover` 展开的菜单；
   katana 用 rod element.Click（CDP Input 真鼠标，含 hover 语义）。可对
   mouseover 类元素补真实鼠标移动。
3. **javascript: href 静态提取**：crawlergo 对 `href^=javascript:` 做 eval 提取；
   我们靠点击自然执行，但被 skip filter 命中的元素会漏。
4. **反爬伪装**：crawlergo 有 webdriver/plugins/chrome 对象伪装；Argo 未做
   （目标有 bot 检测时建议换浏览器指纹方案）。
5. **事件委托广播**：crawlergo 对 click/focus/mouseover 向子节点递归 dispatch
   CustomEvent（处理监听在父级、目标在子级的委托场景）；我们的 el.click()
   天然冒泡已覆盖大部分，但非 click 事件委托仍有差距。
6. **Shadow DOM 内点击**：全行业均未做（我们收集侧已领先）。
