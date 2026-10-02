# 被动扫描器联动（xray push proxy）设计方案

## 背景与目标

对标 crawlergo 的 `--push-to-proxy http://127.0.0.1:1234/`（README「Support pushing the results
to passive web vulnerability scanners」）。被动扫描器（xray `webscan --listen`、w13scan 等）
以代理形式接收流量并自动转发到目标——把爬虫全部流量导过去即完成联动扫描。

## 现状分析

- Argo 已有 `--proxy`（浏览器 launcher.Proxy + Go 侧 GetProxyClient），机制上与 push proxy 相同；
  差别在语义：`--proxy` 是「我的出口代理」，`--pushproxy` 是「镜像给扫描器」，
  两者可能同时需要（用户内网代理 + 扫描器旁路）。
- 浏览器只能设一个代理；上游链由扫描器自己配（xray 支持配置上游代理）。

## 设计

### 参数

CLI `--pushproxy`（string）；config `browser.push_proxy`。

### 行为

- 设置后：**浏览器流量与 Go 侧探测请求全部经 pushproxy**（launcher.Proxy 优先用 pushproxy；
  同时设置 `--proxy` 时警告「上游代理请在扫描器侧配置，浏览器只走 pushproxy」，pushproxy 生效）。
- Go 侧：`pkg/req` getHttpClient / GetProxyClient 增加 pushproxy 分支（优先级高于 proxy）。
- 输出不变——扫描器自己出报告；Argo 日志启动时打印
  `[push] 流量已推送至 http://127.0.0.1:7777（确认扫描器已监听）`。

### 典型用法

```shell
# 终端1：xray 被动监听
xray webscan --listen 127.0.0.1:7777 --html-output argo.html
# 终端2：argo 推送全部流量
./argo -t http://target/ --pushproxy http://127.0.0.1:7777
```

## 影响范围

- `pkg/conf/conf.go`（BrowserConf.PushProxy + MergeArgs）
- `cmd/argo.go`（flag）
- `pkg/engine/engine.go`（InitBrowser 代理选择 + 警告）
- `pkg/req/req.go`（client 代理优先级）

## 风险

1. **扫描器未启动**：浏览器所有请求失败——启动时对 pushproxy 做一次连通性探测
   （TCP dial，失败警告不断言）。
2. **代理链**：不实现浏览器→扫描器→用户代理的双层配置，文档写明在扫描器侧配上游。

## 子任务

- [x] conf/cmd 参数贯通 (BrowserConf.PushProxy + --pushproxy + 冲突警告)
- [x] InitBrowser / req 包代理优先级 (req.EffectiveProxy 统一两侧)
- [~] httptest 假代理单测 (Go 侧 EffectiveProxy 已实现并有既有代理测试覆盖路径；浏览器侧无法在无显示环境断言，留待实测)
