# 限速 / 按 host 限频 / 失败重试 设计方案

## 背景与目标

对标 katana 的 `Delay / RateLimit / HostRateLimit / Retries`（来源：/tmp/katana_src `pkg/types/options.go`）。
真实目标（WAF、限流、CDN）下 Argo 当前是无节流全速访问，容易被封 IP；静态请求失败也无重试。

## 现状分析

- 请求出口有三个：
  1. **tab 导航**（浏览器页面加载，流量主体）：`pkg/engine/tab.go:327` `TabWork` 消费 TabQueue 后直接起 `NewTab`；
  2. **hijack 里的 `ctx.LoadResponse`**：`pkg/engine/engine.go:380`，错误被忽略（rod 返回 error 未接收）；
  3. **静态探测** `pkg/req/req.go`：robots/sitemap/CheckTarget，超时 10s，无重试。
- go.mod（Go 1.18）无 `golang.org/x/time/rate`。按准则「优先复用」，项目内没有同类库；
  x/time/rate 是官方扩展且无传递依赖，引入成本低。**决策：引入 `golang.org/x/time/rate`。**

## 设计

### 新包 `pkg/ratelimit`

```
pkg/ratelimit/
  ratelimit.go   Limiter 与 HostLimiter
```

- `Limiter`：封装 `rate.Limiter`，`rate == 0` 时 Wait 直接返回（不限速，保持默认行为）。
- `HostLimiter`：`map[host]*Limiter` + RWMutex，`Wait(ctx, rawURL)` 解析 host 后按 host 限频；
  解析失败回退全局 limiter。
- 两个实例均为包级单例，`Init(globalRate, hostRate int)` 在 engine 初始化时调用。

### 挂载点

| 位置 | 行为 |
| --- | --- |
| `TabWork`（tab.go:353 起 NewTab 前） | `ratelimit.WaitGlobal(ctx)` + `WaitHost(ctx, uif.Url)`——页面导航是压力主体，节流点放这里 |
| hijack `LoadResponse`（engine.go:380） | 包一层重试：失败后 backoff（500ms×尝试次数）重试，默认 `retry=0` 不重试 |
| `pkg/req`（CheckTarget / 静态 spider） | 同一重试封装 `DoWithRetry(client, request, retries)`，静态探测请求量小但关键（robots/sitemap 是种子） |

注意：hijack 内**不加速率限制**——页面内子资源请求是浏览器行为，在 hijack 里 Wait 会阻塞整个
浏览器流量管道造成死锁风险；限速统一在「我们主动发起」的导航层控制，子资源量随页面数线性下降。

### 配置与命令行

```yaml
browser:
  rate_limit: 0        # 全局每秒最多访问页面数，0 不限
  host_rate_limit: 0   # 单 host 每秒最多访问页面数，0 不限
  retry: 0             # 静态/hijack 请求失败重试次数
```

CLI：`--rate` / `--hostrate` / `--retry`（int，默认 0 = 行为与现在完全一致）。

## 影响范围

- 新增 `pkg/ratelimit`（1 文件 + 测试）。
- 修改 `pkg/engine/tab.go`（TabWork +3 行）、`pkg/engine/engine.go`（LoadResponse 重试 ~10 行）、
  `pkg/req/req.go`（重试封装 ~20 行）、`pkg/conf/conf.go`、`cmd/argo.go`（3 个 flag）、README。
- go.mod 新增 `golang.org/x/time`。

## 风险

1. **限速拖慢默认用户**：默认值 0（不限速），只有显式配置才生效，无默认行为变化。
2. **TabWork 阻塞**：Wait 用 ctx 感知的版本，浏览器超时 cancel 时能立即退出，不影响退出时序。
3. **重试放大请求**：仅对失败请求、上限次数明确、默认 0。

## 子任务

- [x] `pkg/ratelimit` + 单测（限速生效时序 / host 隔离 / rate=0 直通 / ctx 取消）(go test ./pkg/ratelimit/ 通过)
- [x] `pkg/req` DoWithRetry + httptest 失败服务器单测 (go test ./pkg/req/ 通过)
- [x] TabWork 挂载 Wait (tab.go TabWork 起 tab 前 WaitNavigation)
- [x] hijack LoadResponse 重试 (engine.go 失败按 retry 次线性退避重试，错误不再静默)
- [x] conf/cmd 参数贯通 + README (--rate/--hostrate/--retry，默认 0)
- [x] 靶场回归：默认参数检出不变（pikachu 136 条与基线一致）；--rate 2 实测 tab 间隔 ~1s（burst 2）符合预期
