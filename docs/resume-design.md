# 断点续爬（resume）设计方案

## 背景与目标

对标 katana 的 `-resume`：大目标爬到一半（浏览器超时/手动中断/进程被杀）后，
从断点继续而不重复已完成的页面。

## 现状分析

- 全部「已访问」状态在 `pkg/engine/normalize.go` 的 `NormalizeationPendUrlMap`
  （URL 精确 MD5 键，mutex 保护）与 `UrlsQueue`（chan，无法快照）。
- 结果文件已有追加模式（`--mergedOutput` append 家族）。
- 无任何状态持久化：进程结束状态全丢。

## 设计

### 状态文件

`<outputdir>/<save>.state.json`（与结果文件同目录），结构：

```json
{
  "target": "http://a.com/",
  "saved_at": "2026-10-02T23:00:00+08:00",
  "visited": ["<md5>", "..."],        // NormalizeationPendUrlMap 的键集合
  "pending": ["http://a.com/b", ...]  // 已发现未消费的 URL（shadow set）
}
```

### 写入时机

1. 定期：每 10s（后台 goroutine，锁内快照，文件原子写：先写 tmp 再 rename）。
2. 退出前：Finish() 完成后强制一次；`ctrl+c`（SetupCloseHandler）尽力一次
   （signal 处理里加同步 flush，超时 2s 放弃）。

### shadow pending set

`PushUrlQueue` 时登记、`PendUrlWork` 消费或丢弃时移除（锁：与 NormalizeationPendUrlMap
共用一把即可，写入频率低）。resume 后 pending 重新入队。

### 恢复

`--resume <statefile>`：
1. 校验 `target` 与当前 `-t` 一致（不一致 Fatal——去重键不含 target，混用会静默丢正确性）。
2. visited 键批量注入 `NormalizeationPendUrlMap`（这些 URL 不再访问/记录）。
3. pending 逐条 `PushUrlQueue`（重走管线：scope/静态过滤/去重照常，天然清洗过期项）。
4. 结果文件以追加模式续写（用户用 `--mergedOutput` 即自然追加）。

### 登录态（明确限制）

浏览器会话无法持久化：resume 后需重建（--cookie / 自动登录 / --waitlogin）。
文档与 README 写明；状态文件不含任何凭据。

## 影响范围

- `pkg/engine/normalize.go`（map 键导出/导入）
- `pkg/engine/tab.go`（shadow set）
- `pkg/engine/engine.go`（定期保存 goroutine + Finish flush）
- `cmd/argo.go`（signal handler flush）、`pkg/conf/conf.go`

## 风险

1. **状态文件损坏**（进程被杀在写入中）：原子写（tmp+rename）保证旧版本可用。
2. **pending 里的 URL 已失效**：重走管线自然过滤（404/静态/scope）。
3. **多目标**：状态文件按目标+save 名隔离，互不干扰。

## 子任务

- [x] shadow set + 状态快照/原子写 (RegisterPendingShadow/Consume/Snapshot + tmp+rename)
- [x] --resume 加载校验与注入 (目标一致性校验 Fatal；修复两个实测 bug：队列创建前 push 挂死、首页已完成时 FirstPageCloseChan 等待挂死)
- [x] signal flush (cmd SetupCloseHandler 注入 FlushCrawlStateOnExit，2s 上限)
- [x] 验证 (完整跑→resume 零重复访问、正常退出；pending 回放路径与 metadata 推送同路)
