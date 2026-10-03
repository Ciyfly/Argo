# TODO（需求级索引）

规则：每个需求一条，关联设计实现文档；详细子任务在对应设计文档内维护。
两级同步规则见 `~/.claude/CLAUDE.md` 第 8 节。

- [x] JS 接口提取 + 密钥泄漏检测 (设计: docs/js-extract-design.md，完成 2026-10-02，pikachu 回归一致)
- [x] 限速 / 按 host 限频 / 失败重试 (设计: docs/rate-limit-design.md，完成 2026-10-02，默认 0 不改变行为)
- [x] Scope 正则化 + 域外结果输出 (设计: docs/scope-control-design.md，完成 2026-10-02，修复子串误匹配)
- [~] 页面内容相似度去重 (调研: docs/page-similarity-research.md，调研完成待立项)
- [~] classic 轨追赶 katana：分页跟进/表单提交 (作废 2026-10-02：干净测量下两者零缺口——分页与表单已由「跟随明链+auto 表单」覆盖，此前缺口系旧版靶场容器与 sitemap 硬编码 8765 地址造成的测量假象，见 docs/crawl-range-baseline.md)
- [x] classic 轨追赶 katana：交互触发修复与预算优化 (设计: docs/interaction-fix-design.md，完成 2026-10-02：classic 0.785→**0.946 超过 katana 0.899**，spa 0.679→0.764，pikachu 80s/136→67s/165)
- [~] 开源横向对照与提升清单 (调研: docs/oss-comparison.md，2026-10-02 完成调研，候选立项项见文档)
- [x] --waitlogin 人工登录模式 (设计: docs/waitlogin-design.md，完成 2026-10-02，端到端待桌面环境)
- [x] 表单字段级填充规则 (设计: docs/form-fill-design.md，完成 2026-10-02，skip>用户规则>内置语义)
- [x] 被动扫描器联动 pushproxy (设计: docs/scanner-push-design.md，完成 2026-10-02)
- [x] 常见路径探测 path fuzz (设计: docs/path-fuzz-design.md，完成 2026-10-02，165条内置字典默认关)
- [x] 混合引擎 hybrid (设计: docs/hybrid-engine-design.md，完成 2026-10-02：classic 88s→64s 检出 0.946 持平)
- [x] 断点续爬 resume (设计: docs/resume-design.md，完成 2026-10-02，含两处实测挂死 bug 修复)
- [x] 字段级输出 (设计: docs/field-output-design.md，完成 2026-10-02，--fields/--outputtemplate)
- [x] 蜘蛛爬行动画叠加层 (设计: docs/spider-overlay-design.md，完成 2026-10-03，closed shadow 零污染，classic 0.946 无回归)
- [x] Web 控制台 (设计: docs/web-console-design.md + docs/web-projects-design.md，完成 2026-10-03：--web 启动，项目制多目标顺序爬取/结果持久化/三格式导出/URL 实时流/浏览器画面直播含蜘蛛动画)
- [x] 注入/点击 JS 审计与修复 (审计: docs/inject-audit.md，完成 2026-10-03：EvalOnNewDocument IIFE 修复/DOM0 劫持/SPA 路由源头捕获/window.close 锁/定时器降频/XHR 限次，spa 0.793→0.836)
- [ ] 注入审计遗留：iframe 交互/真实鼠标 hover 事件/javascript: 静态提取/反爬伪装 (见 docs/inject-audit.md backlog)
- [x] SPA 路由链四件套 (调研: docs/spa-crawling-research.md + docs/spider-overlay-design.md，完成 2026-10-03：等价重置/链式跟随/骨架哈希/双基准路由，spa 0.836→**0.914**，整场 140s→36s)
- [ ] SPA 登录后动态路由跟进 (待立项：oss-comparison.md Top1，spa_auth 0.154→0.8+)
