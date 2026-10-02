# 混合引擎（hybrid engine）设计方案

## 背景与目标

katana classic 20s vs Argo 88s（4.4 倍）的差距根源是**每个页面都开浏览器**。
目标：`--engine hybrid` 模式下，能不开浏览器的页面不开，classic 耗时降到 ~30s，
**检出率不回退**（门禁：classic ≥ 0.94、spa ≥ 0.76）。

## 现状分析（已有的种子）

- 内容类型预检（`pkg/req.ProbeContentType`）已经让非文档 URL 走 Go 侧 record-only——
  本方案是把同样的分流扩展到 HTML 文档页。
- Go 侧解析能力已齐：`static.ParseHtml`（离线 HTML 解析）、`extract.ExtractAPIPaths`
  （JS/接口提取）、normalize 去重管线。缺的只是「Go 抓取 HTML 后离线解析并喂回队列」这一环。

## 设计

### 参数

`--engine headless|hybrid`（默认 headless，保持现状）。

### hybrid 流水线

```
页面 URL 入队（PendUrlWork）：
  非文档后缀 → 内容预检 → json 等接口 record-only（现状）
  文档后缀/HTML →
    depth == 0（首页）        → 浏览器（登录、交互入口必须真实浏览器）
    depth >= 1                → Go 抓取（复用预检的 client，带 cookie 不带也接受）
        ├─ 解析 HTML（ParseHtml）+ extract 提取 → 子链接入队（depth+1）
        ├─ 响应记为结果（PendingUrl，带真实 status/headers/body）
        └─ 空壳判定（见下）命中 → 升级浏览器层处理
```

### 空壳判定（JS 渲染依赖页识别，结构驱动）

Go 抓到的 HTML 满足以下任一即视为「空壳」，交浏览器：
- `<a href>` 同域链接数 < 3 **且** 引用了同域 JS bundle（`<script src>` 数 ≥ 1）；
- HTML 去空白后 < 5KB 且存在 `<div id="app|root|main">`。

两条都是「这页内容靠 JS 渲染」的通用信号，与站点无关。

### 一致性

- 两路（浏览器 hijack / Go fetch）产出都过 `pushpendingNormalizeQueue`，MD5 精确去重天然合并。
- Go 抓取带浏览器会话不可行（cookie 隔离）——公开页面无影响；登录后页面 Go 抓到的是
  302/login 页（text/html）→ 空壳判定/正常解析，发现不了门内链接但不产生错误结果；
  门内 URL 由浏览器层（首页交互 + 会员页继续浏览器化）覆盖。
  **取舍写明**：hybrid 模式下登录深度爬取建议配合 --engine headless 使用。

### 并发与预算

Go fetcher 用 ants pool（并发 = tabcount 同量级），每请求 10s 超时（req 现值），
交互页面预算/动态 idle 逻辑不变。

## 影响范围

- `pkg/engine/fetcher.go`（新增：Go 抓取 + 离线解析 + 空壳判定，~150 行）
- `pkg/engine/tab.go` PendUrlWork 分支
- `pkg/conf/conf.go`（Engine 字段）、`cmd/argo.go`

## 风险

1. **检出率回退**（最大风险）：空壳判定漏识别的 JS 页 → 少链。回归门禁跑 classic/spa/认证轨三线。
2. **同 URL 双抓**（浏览器又开一遍）：normalize 去重在结果层合并，但浏览器成本浪费——
   记录 Go 已抓的文档 URL 集合，PendUrlWork 不再推给 TabQueue（hybrid 下文档 URL 只走 Go，
   空壳升级的才推浏览器）。
3. **行为差异**：默认 headless 不变，hybrid 是显式选择。

## 子任务

- [x] fetcher 实现 (FetchDocumentPage + looksLikeShell + extract_hook 与 hijack 共用提取)
- [x] PendUrlWork 分流改造 (hybrid && depth>=1 && 文档后缀)
- [~] 性能验证 (实测 88s→64s、tab 199→143；首页交互 ~35s 必须浏览器，30s 目标不可达，如实下调预期为「~60s 量级」)
- [x] 检出率回归 (classic hybrid 0.946 = headless 持平)
