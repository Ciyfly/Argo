# 常见路径探测（path fuzz）设计方案

## 背景与目标

对标 crawlergo 的路径 fuzz（源码 `/tmp/crawlergo_src/pkg/path_expansion.go`）：
内置 ~250 常见目录字典 + 外部字典文件，20 并发，2s 超时，
**2xx 或 301 同 host 即有效**，有效路径进爬取队列（source=fuzz）。
robots.txt 的 Disallow 路径同样回收（很多站点把敏感路径写进 Disallow）。

## 现状分析

- Argo 已有 robots.txt 解析（`pkg/static/robotstxt.go`）——但只取 Allow 语义的路径入队，
  Disallow 路径未利用（robots.go 现状实现只解析了部分）。
- 无任何路径字典探测能力。

## 设计

### 参数

- CLI `--fuzz`（bool，**默认 false**：路径探测本质是主动探测行为，默认关闭降低未授权使用风险）
- `--fuzzdict <file>`：外部字典（每行一个路径），不传用内置字典
- config `fuzz.enable` / `fuzz.dict`

### 内置字典

自编常见目录/入口词表 ~200 条（管理后台、备份、上传、api、git/svn 源码泄露目录等
通用词，不复制 crawlergo 原文）。存放 `pkg/static/fuzzdict.go` 常量，可后续独立成文件。

### 执行流程（Go 侧，不开浏览器）

1. 时机：目标首页访问成功后（拿到 scheme/host 基准），与正常爬取**并行**。
2. 并发：ants pool 20（与 crawlergo 同量级）；每请求 2s 超时；不跟随重定向。
3. 判定有效：`200 <= status < 300`，或 301/302 且 Location 同 host。
4. 有效路径：`sourceType="path fuzz"` 入 UrlsQueue 走现有管线
   （文档类开 tab 扩散，接口类 record-only——内容预检自动分流，**不放大浏览器成本**）。
5. 与限速协同：全局 `--rate` 生效时 fuzz 请求同样过 `ratelimit`（避免 fuzz 触发限流封禁时
   干扰正常爬取，二者共享预算是刻意行为：对目标的总压力受限）。
6. robots Disallow 路径：`--fuzz` 开启时一并入队探测（同一管线）。

### 结果边界

只记 2xx/301 同 host——404/403 不入结果，避免把全站 not-found 刷进输出。

## 影响范围

- 新增 `pkg/static/fuzzdict.go`（字典）+ `pkg/static/fuzz.go`（探测逻辑，~100 行）
- `pkg/engine/engine.go` Start()（首页后触发 goroutine）
- `pkg/conf/conf.go`、`cmd/argo.go`

## 风险

1. **授权边界**：默认关闭 + README 声明与现有「仅教育用途」一致。
2. **对目标的请求放大**：字典 200 条一次性并发 20——有 --rate 兜底 + 每请求 2s 超时。
3. **软 404 误报**（整站 200 的 SPA）：同 host 301 判定 + 文档/接口预检再过滤一层；
   残余误报由用户判断（URL 发现工具的本分）。

## 子任务

- [x] 内置字典(165条) + fuzz 探测实现 + 单测 (TestFuzzPathsValidAndInvalid 三分支断言通过)
- [x] 外部字典加载 + 清洗 (TestFuzzPathsDictFallback)
- [x] robots Disallow 路径回收 (robotstxt.go 本就回收 Allow+Disallow，审查确认无需改)
- [x] 冒烟验证 (165 条并发探测、404/异域301正确过滤、命中入队)
