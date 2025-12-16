# 2025-12-15：URL 来源追踪（SourceType/SourceUrl）贯通输出与落盘

## 时间戳
- 2025-12-15

## 背景与目标
- 目标：让最终输出/落盘的每一条 URL 都能回答两个问题：
  1) **这个 URL 是从哪里“发现/产生”的？**（来源类型 `SourceType`）
  2) **它是从哪个页面/线索推出来的？**（来源 URL `SourceUrl`，通常是父页面/Referer）
- 典型需求：区分 `js_inline/js_file/html_attr/html_form/browser_hijack/interaction_*` 等来源；便于排查误收集、误扩散与误判。

## 设计要点
1) **数据模型扩展**
   - 在结果结构 `PendingUrl` 增加：
     - `SourceType string`
     - `SourceUrl  string`
   - 说明：`SourceType` 表示“发现来源类别”，不等价于“由哪个引擎处理(standard/hybrid)”。历史上 `SourceType` 是自由字符串，本次引入稳定枚举但仍兼容旧值。

2) **稳定枚举**
   - `pkg/engine/source.go` 定义核心来源枚举（示例）：
     - 入口/种子：`homePage/seed/metadata/incremental_resume`
     - 静态解析：`html_attr/html_text/html_comment/html_form/js_inline/js_file/json`
     - 交互/动态：`interaction_auto/interaction_login/interaction_playback/browser_hijack/patch`
     - 其他：`path_climb/resource`（以及 `swagger/graphql/passive_source` 作为模块级来源）

3) **贯通填充链路**
   - StandardEngine：按响应类型区分来源并下发新任务：
     - HTML：`html_attr/html_text/html_comment/js_inline/html_form`
     - JS：`js_file`（JSluice AST + 兜底正则）
     - JSON：`json`
   - Browser(Hybrid) 请求劫持：`browser_hijack`，`SourceUrl` 取当前页面 URL。
   - 页面中间件：
     - 静态 DOM 解析：透传 `static.ParseDomWithSource` 的来源类型
     - 交互链路：区分 `interaction_auto/login/playback`
   - 入口/seed/元数据/路径爬升/资源二次解析：补齐相应 `SourceType` 与 `SourceUrl`。

## 输出与落盘改动
- 终端输出：新增来源展示，形如：
  - `[GET][js_inline] http://a/b <- http://a/page`
- `txt`：输出 `[%method][%source_type]%url\t%source_url`
- `xlsx/html`：新增 `source_type/source_url` 列/字段
- `json/jsonl`：自动包含新字段（保持历史 key 命名风格：`SourceType/SourceUrl`）

## 关键修复（来自真实日志）
- `meta` 的 `content="text/html; charset=..."` 不再被误判成 URL（仅在 `content` 含 `url=` 时解析 meta refresh）。
- 相对资源名（如 `index.php/style.css`）不再被误判成裸域名导致 `http://index.php/` 这类无效请求；在 `utils.CanonicalizeURL` 增加常见扩展名兜底。

## 变更文件清单
- `pkg/engine/source.go`
- `pkg/engine/normalize.go`
- `pkg/engine/result.go`
- `pkg/engine/sink.go`
- `pkg/engine/template.go`
- `pkg/engine/tab.go`
- `pkg/engine/dual_engine.go`
- `pkg/engine/pipeline.go`
- `pkg/engine/interaction.go`
- `pkg/engine/engine.go`
- `pkg/engine/resource_enricher.go`
- `pkg/static/parse.go`
- `pkg/utils/urlcanon.go`
- `pkg/static/parse_html_test.go`
- `pkg/utils/urlcanon_test.go`

## 验证证据
- `go test ./...` 通过。
- 轻量运行验证（示例）：
  - `./bin/argo-dev -t http://testphp.vulnweb.com/ --debug --tabcount 1 --maxdepth 1 --browsertimeout 40 --tabtimeout 20 --tabsofttimeout 10 --maxretries 0 --format txt,json --save demo_source --outputdir ./result/testphp.vulnweb.com --norrs`
  - 结果文件 `result/testphp.vulnweb.com/demo_source.txt` 中可见 `js_inline/browser_hijack/html_attr` 等来源字段。

## 回滚方案
- `git checkout -- pkg/engine/source.go pkg/engine/normalize.go pkg/engine/result.go pkg/engine/sink.go pkg/engine/template.go pkg/engine/tab.go pkg/engine/dual_engine.go pkg/engine/pipeline.go pkg/engine/interaction.go pkg/engine/engine.go pkg/engine/resource_enricher.go pkg/static/parse.go pkg/utils/urlcanon.go pkg/static/parse_html_test.go pkg/utils/urlcanon_test.go`

