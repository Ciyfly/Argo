# AutoJS 交互桥与动态超时改造方案（草案）

## 背景与目标
- 现状：AutoJS 运行固定 15s 超时，动作上限 200、节奏 `slow` 默认 1s，复杂页面必超时且日志缺乏细粒度反馈。
- 目标：通过 JS↔Go 实时通信与分段执行，做到可观测、可中断、可调节，超时与页面复杂度自适应；先落地设计与任务拆分，逐步实施。

## 核心方案概述
1. **注入轻量消息桥 `window.argoBridge`**  
   - JS 侧：提供 `argoBridge.emit(event, payload)`、`argoBridge.waitContinue()`；通过 `window.postMessage` 向上报事件。  
   - Go 侧：使用 rod 的 Binding（`page.Expose`/`AddBinding`）或 `Page.EachEvent(proto.RuntimeBindingCalled)` 订阅事件，必要时向页面发 `postMessage` 回指令（如 `continue/stop/update_slow`）。
2. **分段执行（批次执行模型）**  
   - AutoJS 拆为“收集 → 批次执行 → 上报 → 等待指令”循环。每批默认 20 个动作，批间可调整 `slow` 或提前停止。  
   - 批次上报内容：动作数、成功/失败、累计耗时、剩余队列、发现 URL 数、最近点击 selector/文本。
3. **动态超时协商**  
   - JS 在首批前估算预算：`estimated_ms = min(actionLimit, queue_len) * slow + base(2s)`；通过事件 `budget_proposal` 告知 Go。  
   - Go 侧将 `eval` 超时设为 `min(tabtimeout-5s, estimated_ms*1.5 + safety)`；仍保留 tab 级硬超时 `tabtimeout`。  
   - 若 Go 未响应预算，则使用 `max(estimated, 15s)` 兜底。
4. **可中断与降级**  
   - Go 在批次间可发 `stop` 指令立即返回当前结果；若桥异常，回退到单次 `Eval`（兼容现状），并在日志标记降级。
5. **日志与观测**  
   - 所有桥事件在 Go Debug 日志打印：`[auto-event] target=... evt=... payload=...`。  
   - 统计写入 `MetricsSummary`：新增 `auto_batches`, `auto_actions`, `auto_time_ms`, `auto_bridge_errors`。

## 通信协议（初稿）
JS → Go（事件名 `event`，载荷 `payload`）：
- `budget_proposal`: `{estimated_ms, action_limit, slow}`  
- `batch_done`: `{batch_id, actions, success, fail, duration_ms, queue_left, urls_found, last_selector, last_reason}`  
- `final_result`: `{urls, logs, total_actions, total_ms}`  
- `error`: `{stage, message}`  

Go → JS（消息 `command`）：
- `continue`: `{batch_size?, slow?}`（允许动态调整批大小/节奏）  
- `stop`: `{reason}`  
- `set_slow`: `{slow}`  

## 代码改造任务拆分
P0（先行实现）
1. **桥接注入**：在 `inject.InjectScript` 之前/之后插入桥脚本，封装 `argoBridge.emit/waitContinue/post`；页面卸载时自动清理事件监听。  
2. **Go 订阅管道**：在 `EngineInfo.NewTab` 创建页面后，注册 `RuntimeBindingCalled` 监听 & 通过 `RuntimeEvaluate` 发送命令，保持线程安全的事件消费与日志打印，同时将 AutoJS 的超时估算回传至 `ExtendTimeout`。  
3. **AutoJS 批次执行重构**：将现有 `auto()` 改为批循环，支持预算估算与动态 `slow`；批次间等待 `continue`（带超时，超时则自行继续但记录“no-ack”）。  
4. **动态 eval 超时**：`evalAutoWithRetry` 接收预算参数，调用 `page.Timeout(adaptive)`；异常时记录 `auto_bridge_errors++` 并回退到单次 eval。  
5. **配置入口**：新增 CLI/配置 `auto.batch_size`（默认 20）、`auto.bridge_debug`（默认 false），并向下传入 JS。

P1（增强）
6. **细粒度统计与 `/metrics` 输出**：扩展 `MetricsSummary` & `/metrics` JSON，加入 auto 相关指标。  
7. **可视化日志格式**：统一 `[auto-event]` 日志格式，方便 grep。  
8. **降级报警**：当桥失败或超时回退时输出 WARN，并附带建议（调大 tabtimeout/slow）。

P2（体验优化）
9. **元素筛选优化**：加入可见性/禁用状态检查、修正 `logout` 过滤、视窗内优先。  
10. **表单安全模式**：在非登录场景仅读不写；登录场景再填充。  

## 里程碑与验收
- **里程碑1**：桥注入 + 批次执行跑通，能在 debug 日志看到 `budget_proposal` 与多批 `batch_done`，无回退。  
- **里程碑2**：动态超时生效，长页面不再出现 15s `context deadline exceeded`，tab 级超时才触发关闭。  
- **里程碑3**：指标写入 `/metrics`，包含 auto 批次与耗时。  

## 风险与缓解
- 站点 CSP 阻止 `postMessage` 或 `addEventListener`：同源内仍可用；若被阻断自动降级至 console 方案并提示。  
- 频繁批次通信导致性能下降：可配置批大小/节奏，默认仅 20 次动作一次上报。  
- Rod 版本兼容：若当前 rod 版本缺少 `Expose`，退回 `Runtime.AddBinding` 的底层调用。
- Tab 在 AutoJS 尚未结束时触发 `tabtimeout`：建议在 CLI/config 中提供 `browser.tab_timeout` 的较大默认值，并以 AutoJS `final_result`/`stopped` 事件驱动 Tab 关闭；后续可按队列长度或批次耗时动态调整剩余预算。

## 扩展建议（后续迭代）
- **Tab 超时治理**：引入“软超时 + 硬兜底”的双层策略。软超时基于 AutoJS 报告的 `queue_left`/`duration_ms` 动态续期（每个 batch 都发 `extend_ms`）；硬兜底保持较大值（默认 240s）防止失控。AutoJS 完成后主动通知 Go 关闭 Tab，避免长时间空转。
- **随机 Slow/人类化操作**：允许在配置中声明 `slow_min/slow_max` 或 `slow_jitter_pct`，由 JS 在每次动作时随机等待，减少被行为风控识别的风险。
- **配置字段**：新增 `browser.tab_soft_timeout`（软超时，默认 60s）、`browser.tab_timeout`（硬上限，默认 240s）、`auto.slow_min` / `auto.slow_max`（随机延迟区间，默认 600~1400ms），并在运行期根据 AutoJS 估算（`action_limit * avg(slow_range)`）动态续期 Tab。

## 下一步
- 依据本方案实现 P0 任务，代码提交前补充使用说明到 README/架构文档，并添加最小集成测试（mock Page + 事件驱动）。 

## 当前实现状态（2025-11-16）
- **配置默认值**：`configs/config.yml` 与 `pkg/conf/conf.go` 已将 `browser.tab_soft_timeout` 设为 60 秒、`browser.tab_timeout` 设为 240 秒，同时开放 `auto.slow_min=600ms`、`auto.slow_max=1400ms` 作为随机延迟区间，并在 `MergeArgs` 中做兜底。
- **桥接事件**：AutoJS 通过 `budget_proposal`、`batch_done`、`final_result` 持续上报 `extend_ms`，Go 端在 `pkg/inject/bridge.go` 中解析并调用 `PageContext.ExtendTimeout`，确保软超时不断续期。
- **Tab 控制**：`pkg/engine/tab.go` 实现软/硬双计时器，软计时默认 60 秒，可被 AutoJS 的 `extend_ms` 重置；硬计时默认 240 秒，到期必关 tab 以避免泄漏。
- **随机节奏**：`pkg/inject/auto.go` 在每次动作时按 `slow_min~slow_max` 随机等待，并将平均值纳入预算估算，避免固定 1s 带来的可识别性和超时风险。
