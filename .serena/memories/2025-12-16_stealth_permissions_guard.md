# 2025-12-16：Stealth 脚本健壮性修复（permissions/userAgent 等容错）

## 时间戳
- 2025-12-16

## 背景与现象
- 历史日志中出现 `Inject stealth script error`，典型报错：
  - `TypeError: Cannot read properties of undefined (reading 'apply')`
- 触发条件通常是页面/浏览器环境缺失某些 API（如 `navigator.permissions.query`）或返回值并非函数，导致脚本执行中断，进而影响后续反检测与页面交互稳定性。

## 根因分析
1) **permissions API 假设过强**
   - 旧脚本默认 `navigator.permissions.query` 存在且为函数，缺失时会在覆写/调用阶段报错。
2) **userAgent getter 存在递归风险（基础 stealth 脚本）**
   - 在 getter 内再次读取 `navigator.userAgent` 会导致递归调用。

## 解决方案
- 对关键 API 增加“存在性判断 + try/catch”：
  - `navigator.permissions` / `permissions.query` 缺失时走安全降级（返回 prompt）。
  - WebGL、Canvas 等 API 不存在时跳过对应补丁，避免整段脚本失败。
- `userAgent` 改为捕获原始值后再替换，避免递归。

## 变更文件清单
- `pkg/engine/stealth.go`
- `pkg/engine/stealth_enhanced.go`

## 验证证据
- `go test ./...` 通过。

## 回滚方案
- `git checkout -- pkg/engine/stealth.go pkg/engine/stealth_enhanced.go`

