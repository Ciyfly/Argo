# 2025-12-16 路径无限叠加（/static/static/...）修复记录

## 前置说明（偏差与工具）
- Serena MCP：当前环境未接入，无法按“Serena 优先”执行结构化检索/编辑；因此按降级矩阵使用 Codex CLI 的 `apply_patch` 与本地 `go test` 完成修复与验证。
- 外部证据：来自用户反馈的运行结果与 URL 列表（例如 `http://10.199.0.134/static/static/static/...`、`.../width-device-width,initial-scale=1,...`、`.../IE=edge,chrome=1`、`.../webkit` 等）。
- 复现限制：容器内无法连通 `10.199.0.134`（连接超时），因此采用“构造 HTML/JS 单测”复现与验证。

## 问题描述
- 现象：扫描过程中 URL 路径出现“无限叠加/爆炸”，典型为 `/static/static/static/...`；同时把 `viewport / X-UA-Compatible / renderer` 等 meta 配置串误当成 URL，导致生成诸如：
  - `/width-device-width,initial-scale=1,maximum-scale=1,user-scalable=no`
  - `/IE=edge,chrome=1`
  - `/webkit`
- 影响：调度队列持续增长，任务难以收敛，表现为“卡住/跑不完”。

## 根因分析（归因）
1) HTML 相对路径解析未识别 `<base href="...">`：在 SPA fallback 场景下，同一份 index.html 可能在不同目录（如 `/static/`、`/static/static/`）返回；若资源引用写成 `static/...` 或 `.xxx.js` 等相对路径，会被按“当前目录”解析，导致路径不断叠加。
2) JS AST 提取（JSluice）对 URL 判定过宽：把不具备 URL 结构的字符串（如 `webkit`、`IE=edge,chrome=1`、`width=device-width,...`）也当成 URL 候选，进一步制造无效路径并参与叠加。
3) Canonicalize 侧缺少“路径爆炸”兜底：对 `static/static/...` 这类连续重复目录段没有折叠/限流，导致去重前就产生大量新 URL。

## 修复内容（关键变更）
- HTML 解析支持 `<base href>`：在 `ParseHtmlWithSource` 中识别并应用 base href 作为相对 URL 解析基准（不把 `<base>` 当作可爬 URL 输出），避免 SPA fallback 触发的路径叠加。
- JS URL 有效性过滤增强：JSluice 的 `isValidURL` 增加“URL-ish”结构判断（需具备 `http(s)://`、`/`、`.`、`?` 等特征），过滤 meta 指令串与无意义 token。
- CanonicalizeURL 增加防护：
  - 过滤 viewport/X-UA-Compatible 等“非 URL 指令串”（含 `=`/`,` 且不含 URL 结构）。
  - 折叠常见静态目录段（`static/assets/public/dist/build`）的连续重复，避免 `/static/static/...` 爆炸。

## 变更文件清单
- `pkg/static/parse.go`
- `pkg/static/parse_html_test.go`
- `pkg/static/jsluice.go`
- `pkg/static/jsluice_test.go`
- `pkg/utils/urlcanon.go`
- `pkg/utils/urlcanon_test.go`
- `README.md`

## 验证方式与结果
- 本地执行：`go test ./...` 通过。
- 新增单测覆盖：
  - `<base href="/">` 在 `/static/` 场景下避免解析出 `/static/static/...`
  - JSluice 过滤 `webkit / IE=edge,chrome=1 / width=device-width...`
  - Canonicalize 折叠 `static/static/...` 并过滤 meta 指令串

## 回滚思路
- 直接 `git revert` 本次提交或回退到前一版本；若仅需临时规避可将 base href/折叠逻辑开关化（后续如有需求再做）。

