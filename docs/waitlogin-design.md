# --waitlogin 人工登录模式 设计方案

## 背景与目标

对标 rad 的 `-wait-login`（README：自动禁用无头、开浏览器供手动登录、登录完按回车继续）。
Argo 明确不支持验证码登录（README 声明）；人工登录模式是验证码/短信/扫码场景的通用解：
人完成登录，爬虫带着会话继续爬。

## 现状分析

- `pkg/engine/engine.go InitBrowser`：`launcher.New().NoSandbox(true).Headless(true)`，
  有头仅当 `UnHeadless || Dev`（engine.go:234-237）。
- 首页处理在 `pkg/engine/tab.go NewTab` 的 `HOME_PAGE_FLAG` 分支：load → playback → login.Handler → Auto。
- 页面存活由 idle tracker + 硬上限控制（tab.go 外层 select）——等待人工操作期间会误杀。

## 设计

### 参数

CLI `--waitlogin`（bool，默认 false）；config `browser.wait_login`。

### 行为

1. `waitlogin` 开启时强制有头（`options.Delete("--headless")`，与 --uh 等价但语义独立），日志提示。
2. 首页（HOME_PAGE_FLAG）加载完成后、自动登录/交互**之前**：
   - 终端打印 `[wait-login] 请在浏览器中完成登录，然后回到终端按回车继续爬取...`
   - 阻塞读 stdin 一行（`bufio.NewReader(os.Stdin)`），仅此一次（`sync.Once`，多目标时每目标一次）。
   - 等待期间每秒 `tabProgress.Mark()` 一次，防止 idle/硬上限误杀首页。
3. 回车后：重置进度计时 → 正常走后续管线（自动登录会跳过——已登录态；Auto/收集照常）。
4. **非交互环境保护**：stdin 不是终端（`os.Stdin Stat().Mode()&os.ModeCharDevice == 0`，如 CI/管道）时，
   警告 `stdin 非终端，跳过人工登录等待` 并继续，不阻塞。
5. 与 `--playback` 互斥：playback 本身就是登录脚本，同时给 waitlogin 时 playback 优先并警告。

### 登录态延续

人工登录写入的 Cookie 存在于浏览器（无痕窗口）内，本目标全程有效；
多目标串行时每个目标重开浏览器，需要每目标各登录一次（文档写明）。

## 影响范围

- `pkg/conf/conf.go`（BrowserConf.WaitLogin + 默认 yaml + MergeArgs）
- `cmd/argo.go`（flag）
- `pkg/engine/engine.go`（InitBrowser 有头分支）
- `pkg/engine/tab.go`（HOME_PAGE 分支等待逻辑 ~20 行）

## 风险

1. **无人值守管道下挂死**：非终端检测兜底；等待期间仍有浏览器总超时（browserTimeout）硬上限。
2. **等待期间 idle 误杀**：等待循环内每秒 Mark，已覆盖。

## 子任务

- [x] conf/cmd 参数贯通 (BrowserConf.WaitLogin + --waitlogin)
- [x] InitBrowser 有头强制 + stdin 终端检测 (stdinIsTerminal)
- [x] HOME_PAGE 等待 + 进度保活 (waitManualLogin，等待期间每秒 Mark)
- [~] 靶场验证 (本环境无 X server 无法起有头浏览器，端到端待桌面环境验证；代码路径与管道跳过分支已审查)
- [x] 管道模式设计上跳过等待（stdin 非终端检测），环境限制未跑通有头路径故未端到端
