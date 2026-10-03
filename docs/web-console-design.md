# Web 控制台（任务创建 + URL 流 + 浏览器画面直播）设计方案

## 背景与目标

`argo --web` 启动内置 Web 控制台：
- 顶部：创建爬虫任务（目标 URL + 常用参数）、启动/停止
- **左侧**：爬取到的 URL 实时列表（滚动 feed）
- **右侧**：浏览器画面直播——**能看到蜘蛛爬过去点元素的动画**

## 架构

```
浏览器(用户的) ←HTTP← argo 内置 web server ←─ argo 进程内
  /                嵌入式 HTML 控制台页面
  /api/stream      MJPEG 流（当前活动 tab 截图循环）
  /api/results     SSE，URL 结果增量推送
  /api/task        POST 创建/停止，GET 状态
                    │
                    ├─ engine.Run(target)  goroutine（单任务并发）
                    ├─ 无头 Chrome 照常爬取；web 模式下注入蜘蛛 overlay
                    └─ engine 包暴露钩子（OnTabOpen / SnapshotResult / StopCurrent）
```

### 关键决策

1. **画面用 MJPEG 而不是 noVNC/CDP screencast**：
   `<img src="/api/stream">` 直接渲染 multipart JPEG 流，零前端依赖、实现 ~50 行；
   目标 ~6-8 fps（JPEG 截图单帧 ~40-80ms），看蜘蛛动画足够流畅。
   noVNC 需要给 Chrome 配 Xvfb（重）；CDP screencast 需要 ack 语义（复杂度不值）。
2. **蜘蛛注入条件扩展**：web 模式虽然无头，但截图能渲染 CSS 动画（已实测），
   注入条件增加 `WebConsole`。无头注入对爬取逻辑零影响（closed shadow 隔离已验证）。
3. **跟随当前活动 tab**：多 tab 并发时画面跟随「最近打开且还活着」的页面；
   截图失败（页面已关）自动切到最新 tab。
4. **单任务并发**：engine 的结果列表/队列是包级全局状态，多任务并行需要大重构
   （v1 不做）；第二个任务在运行中被拒绝。
5. **停止语义**：engine 增加 `StopCurrent()`（cancel ctx + 关浏览器触发 Finish 收尾）。
6. **安全**：默认只监听 127.0.0.1；--webaddr 可改。本机工具定位，无鉴权（README 声明）。

## 实现

### 新增 `pkg/web/`（server + 嵌入式页面）

- `console.go`：
  - `Deps{ StartTask func(target string, opts TaskOptions) error; StopTask func(); Status func() TaskStatus; SnapshotResults func() []*engine.PendingUrl; ... }`——依赖注入避免 import 环（engine 不 import web）
  - 页面注册表：`RegisterPage(p)` / `CurrentPage()`（mutex）
  - MJPEG：循环 `page.Screenshot(jpeg)` → multipart 写出；客户端断开即停
  - SSE：每 500ms 轮询 SnapshotResults，按索引增量推 JSON 行
- `console.html`（//go:embed）：深色科技风单页（与蜘蛛同风格），原生 JS

### engine 侧小改

- `var OnTabOpen func(*rod.Page)` 钩子（NewTab 页面创建成功后调用）
- `Run` 拆出可停止：包级 `currentCancel/currentEngine`（mutex），`StopCurrent()`
- `ResetResult` 时递增 `resultGen`，导出 `ResultGen()`——web 侧检测新任务重置 SSE 游标
- 蜘蛛注入条件：`UnHeadless || Dev || WaitLogin || WebConsole`

### conf/cmd

- `--web`（bool，启动控制台并阻塞）、`--webaddr`（默认 127.0.0.1:8088）
- Conf.WebConsole bool / WebAddr

## 影响范围

- 新增 pkg/web/{console.go, console.html}
- engine.go（钩子 + StopCurrent + Run 重构 + 蜘蛛注入条件 + resultGen）
- tab.go（OnTabOpen 调用）、conf、cmd、README

## 风险

1. **截图与爬取争抢**：截图走 CDP 截图不进 DOM/网络，对爬取无干扰；CPU 有少量开销（tabcount 大时可降帧率）。
2. **StopCurrent 的收尾竞态**：cancel+关浏览器后 Finish 依赖超时兜底（browserTimeout 内退出）；web 侧停止按钮标记「停止中」直到 done。
3. **页面翻转频繁**：跟随最新 tab，快页面会闪——可接受（v1）；后续可加 tab 选择器。
4. **SSE 断线**：浏览器 EventSource 自动重连，游标按 gen 重置。

## 子任务

- [x] engine 钩子（OnTabOpen/StopCurrent/ResultGen/RunAsync）+ Run 重构
- [x] pkg/web server（任务 API + SSE + MJPEG ~7fps + 嵌入式深色控制台页面）
- [x] 蜘蛛注入条件扩展（无头 web 模式同样注入）+ --web/--webaddr
- [x] 端到端验证：页面 200、任务 API、SSE 增量、MJPEG 每帧含蜘蛛霓虹像素（56-58/帧）、停止 ≤4s 完成（修复 urlsQueueEmpty 取消死锁）、停止后可再启新任务
- [x] classic 回归 0.940（波动带内）
