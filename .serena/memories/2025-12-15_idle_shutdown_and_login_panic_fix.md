# 2025-12-15：修复空闲误判/收尾卡住 + 登录交互超时 panic

## 时间戳
- 2025-12-15

## 背景与现象
- 现象1：运行 `./bin/argo-linux-amd64 -t http://testphp.vulnweb.com/ --debug` 后长时间“卡住”，直到 `browser_timeout` 才退出。
- 现象2：在短跑压测中复现 `panic: context canceled`，堆栈指向登录交互里对 Rod `Must*` API 的调用（Tab 超时/Cancel 后仍在执行）。

## 根因分析
1) **Finish() 对 `FirstPageCloseChan` 的硬依赖不再成立**
   - 旧逻辑：只有浏览器 Tab（`closeTab`）会向 `FirstPageCloseChan` 写入。
   - 新架构：`DualEngine` 可能把首页路由到 `StandardEngine`，导致首页不经过浏览器 Tab，`FirstPageCloseChan` 永远不会被触发，从而 `Finish()` 卡住直到全局超时。

2) **登录交互在超时取消后仍使用 `MustEval` / `MustSelectAllText`**
   - `ctx.Page.Timeout(...).CancelTimeout()` 会取消 Rod 上下文。
   - `Must*` 方法在遇到 `context canceled` 时直接 panic，导致进程被打崩。

3) **收尾顺序存在风险**
   - 超时路径历史上会走 `Close()`（内部曾调用 `SaveResult()`），而 `Start()` 结束处又调用一次 `SaveResult()`，存在重复关闭结果管道的风险。
   - 结果管道未等待消费完成就落盘，可能导致 `ResultList` 不完整。
   - 收尾阶段关闭 Normalize 队列与生产并发时有 `send on closed channel` 风险。

## 解决方案摘要
- 引入“**全局空闲快照 + 稳定窗口**”判定：
  - Scheduler：增加 `SchedulerSnapshot` 与 `activeTabs` 统计。
  - StandardEngine：增加 `inflight` 与 `StandardEngineSnapshot`。
  - 后台发现任务：用计数器纳入空闲判断。
  - `Finish()` 改为 `waitGlobalIdle`，不再依赖 `FirstPageCloseChan`。
- 收尾流程重排：
  - `Finish()` 进入收尾后设置 `stopping`，关闭 Normalize 队列并等待其退出，然后停止模块/引擎（含 `DualEngine.Stop()`）。
  - `SaveResult()` 先关闭 `ResultQueue` 并等待消费完成，再落盘，避免结果不全/重复 close。
  - Normalize 队列推送增加 `recover()` 兜底，避免收尾并发关闭导致崩溃。
- 登录交互稳定性：
  - 移除 `MustEval` / `MustSelectAllText`，改为返回错误的 API 并容错处理，避免 `context canceled` 直接 panic。
- Tab 早退修复：
  - `CheckTarget` 失败改为走 `NormalCloseTab`，确保释放浏览器/池槽位，避免资源泄漏影响收尾判断。

## 变更文件清单
- `pkg/engine/idle.go`
- `pkg/engine/engine.go`
- `pkg/engine/scheduler.go`
- `pkg/engine/dual_engine.go`
- `pkg/engine/result.go`
- `pkg/engine/normalize.go`
- `pkg/engine/tab.go`
- `pkg/login/match.go`
- `pkg/login/login.go`

## 验证证据
- `go test ./...` 通过。
- 短跑验证（示例）：
  - `go run ./cmd -t http://testphp.vulnweb.com/ --debug --tabcount 2 --maxdepth 1 --browsertimeout 180 --tabtimeout 60 --tabsofttimeout 30`
  - 观测到输出 `[idle candidate]` 与 `global idle`，随后模块正常 stop/close、结果落盘；不再出现 `panic: context canceled`。

## 回滚方案
- `git checkout -- pkg/engine/idle.go pkg/engine/engine.go pkg/engine/scheduler.go pkg/engine/dual_engine.go pkg/engine/result.go pkg/engine/normalize.go pkg/engine/tab.go pkg/login/match.go pkg/login/login.go`

