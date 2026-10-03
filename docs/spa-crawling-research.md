# SPA 爬取的开源与学术前沿调研（对照我们的三次实测）

调研时间：2026-10-03。背景：SPA 0.836 后尝试两种"路由追链"方案均失败
（视图循环 0.750 / history.back 0.757），本文梳理业界与学术界的处理方式，
并结合我们已实测的数据给出可落地的方向。

## 一、学术正统：Crawljax 的状态流图（Mesbah & van Deursen，TU Delft/UBC）

SPA 爬取的奠基性工作（[Mesbah 博士论文](https://research.tudelft.nl)、
[Crawljax](https://github.com/crawljax/crawljax)，556★，OWASP ZAP 的 AJAX
Spider 即基于它）。核心思想：**SPA 不是页面集，是状态图**。

- **状态流图（SFG）**：节点 = DOM 状态，边 = 事件（可点元素触发）。
- **stripped DOM 状态判定**：剥离易变内容（时间戳/随机 id）后比较 DOM，
  判定"新状态"还是噪声——比我们的"任意 Mutation 即 changed"精确得多。
- **状态恢复 = 重放事件路径**（源码确认，`Crawler.reset(nextTarget)`）：
  1. 先尝试**等价重置**——单事件回初始态（如点 logo/首页链接，省整页重载）；
  2. 否则 `goToUrl(url)` 整页重载 + **重放记录的 CrawlPath（事件序列）**到目标态，
     再触发下一个事件继续探索。
- **状态抽象可插拔**：DOM hash / Levenshtein / RTED（树编辑距离）/
  TLSH（局部敏感哈希）/ 视觉哈希——"相似状态"判定的多个前沿实现。
- backtrackTarget 配置限制回放深度；reachedNearDup 处理近似重复态。

**代价**：重放是 O(路径长) 的整页重载，Crawljax 以慢著称——它的定位是
"测试覆盖/建模"，不是速度。

**与我们三次实测的对照**（关键洞察）：Crawljax 证明了"回退到上个 URL 没用，
SPA 状态不可 URL 寻址，必须重放事件序列"。我们的 backToStart 是**全页重载
但不重放**——所以链断；视图循环/history.back 是不重载但也没重放——
应用状态仍在旧视图、新视图上没有旧序列可续。Crawljax 的完整重放
（重载 + 按序重点）在我们的 pass2 里**机制上已部分存在**：pass2 对同一按钮
重复点击 = 重放，且每轮路由变化会被 __argoRoutes 收集为进展。链断的真正
瓶颈是每次 backToStart 重载 2-4s × 链长，预算（35s/页）在链走完前耗尽。

## 二、生产界：Googlebot 与商业爬虫

- **Googlebot 两波抓取**（[linkbot 分析](https://library.linkbot.com)、
  [stackmatix](https://www.stackmatix.com)）：第一波抓原始 HTML 的静态链接；
  第二波异步渲染执行 JS——渲染波**只提取 `<a href>` 等静态内容，不点击交互**。
  即最强爬虫对 SPA 的策略反而是"渲染但不交互"，把交互留给站点方做 SSR。
- SSR/预渲染是站点侧的解法（[AWS 模式](https://repost.aws)、
  [Ember FastBoot 论文](https://www.theseus.fi)）。
- 商业工具：Crawlee/Playwright 交给用户写逻辑（[crawlee.dev](https://crawlee.dev)）；
  历史方案：`#!` hashbang + `_escaped_fragment_`（[sitepoint](https://www.sitepoint.com)，
  已废弃）。

## 三、前沿（2024+）

- **LLM Web Agent**（当前最热）：[AutoCrawler](https://arxiv.org)
  （arXiv 2024，LLM 生成爬虫规则的两阶段框架）、
  [DOM Downsampling for LLM Web Agents](https://arxiv.org)（2025）、
  [Budget-constrained Web Collection Agent](https://arxiv.org)——让 LLM 读
  DOM 决定点什么，本质是"语义驱动的 Crawljax"。前沿但慢且贵。
- [Speeding Up High-Fidelity Crawling of the Modern Web](https://collaborate.princeton.edu)
  （Princeton 2024）：资源级抓取效率。
- [APIMiner](https://www.mdpi.com)（2024）：页面状态相似性驱动的动态遍历。

## 四、对我们（结合实测）的可落地结论

1. **等价重置**（Crawljax，低成本）：backToStart 优先找"单事件回首页"的等价
   （logo/首页链接 click，同文档路由不重载），省 2-4s/次的重载。可做。
2. **链式模式定向跟随**（backlog 已记）：URL 呈同前缀递增模式（/wizard/step-1→2→3）
   判定为链后专路跟随、不回起点。注意 0.750 的教训——仅对识别为链的路由生效，
   普通路由照旧回起点。
3. **stripped DOM 状态哈希**（Crawljax）：比"任意 Mutation"精确的 noProgress 判定，
   顺带解决"页面有 CSS 动画则 changed 永真"的问题。中等工作量。
4. **JS bundle 里的静态路由**：SPA 路由表通常定义在 JS 里（我们 js extract 已
   部分覆盖——SPA 缺失项中多数路由已在结果里，缺的是"访问"），配合路由表
   直接 Visit 而非点击推进，可作为兜底补充。

## 来源

- [Crawljax GitHub](https://github.com/crawljax/crawljax)（556★）及源码
  （/tmp/crawljax，Crawler.reset/CrawlPath/StateVertexFactory）
- [Software Analysis for the Web (Mesbah et al.)](https://people.ece.ubc.ca)
- [XIEv: Dynamic Analysis for Crawling Web Applications (ACM 2020)](https://dl.acm.org)
- [AutoCrawler (arXiv 2024)](https://arxiv.org)、
  [DOM Downsampling for LLM Web Agents (arXiv 2025)](https://arxiv.org)、
  [Budget-constrained Web Agent (arXiv)](https://arxiv.org)
- [Speeding Up High-Fidelity Crawling (Princeton 2024)](https://collaborate.princeton.edu)
- [APIMiner (MDPI 2024)](https://www.mdpi.com)
- Googlebot 两波抓取：[linkbot](https://library.linkbot.com)、
  [stackmatix](https://www.stackmatix.com)
- [AJAX crawlable 历史（sitepoint）](https://www.sitepoint.com)、
  [Lumar SPA crawling](https://www.lumar.io)、[crawlee.dev](https://crawlee.dev)

## 四项全部落地（2026-10-03 实现 + 实测）

| # | 项目 | 实现 | 实测 |
|---|---|---|---|
| ① | 等价重置 | backToStart 优先点击 href≈起点的链接（SPA 同文档路由不重载），找不到再 Navigate | **整场爬取 140s 超时 → 36s 自然完成** |
| ② | 链式模式跟随 | 点击落空（签名+xpath 都找不到）即回起点恢复视图；路由跳转留在新视图由 pass2 重复点击沿链推进，连续两轮无新路由回起点 | **spa 0.836→0.914（wizard/onboard/checkout 链可走）** |
| ③ | stripped DOM 骨架哈希 | 标签树骨架（≤5000 节点、深度 14，djb2）判定 changed/same——CSS 动画/定时器改样式不再永真 | 修复 MutationObserver 方案"changed 永真"缺陷 |
| ④ | JS bundle 双基准路由 | 路由表 path 无部署前缀（Vue Router base）时补按应用根（assets 上一级）解析的变体，错的被 404/预检过滤 | shop/wizard 组路由命中 |

最终：**spa 0.914（开局 0.621，累计 +47%）**，classic 0.940 波动带内，整场 spa 耗时 140s→36s。
