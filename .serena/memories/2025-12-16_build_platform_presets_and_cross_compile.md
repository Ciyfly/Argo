# 2025-12-16 build.sh 平台快捷参数 + 多平台可构建修复

## 前置说明（偏差与工具）
- Serena MCP：当前环境未接入，无法按“Serena 优先”执行结构化检索/编辑；因此按降级矩阵使用 Codex CLI 的 `apply_patch` 与本地 `go test/go build` 完成修改与验证。

## 背景与问题
- 需求：`scripts/build.sh` 增加“平台快捷参数”，默认 `linux/amd64`；支持 `windows`、`arm64`；`all` 构建全版本；不传参数即 `linux/amd64`。
- 发现的阻塞：原先交叉编译 `windows/amd64`、`linux/arm64` 会失败，原因是 JSluice 依赖 tree-sitter（cgo），交叉编译默认禁用 cgo，导致依赖包 “build constraints exclude all Go files”。

## 方案与改动
1) **JSluice 按 cgo 条件编译**：
   - 将 `pkg/static/jsluice.go` 与 `pkg/static/jsluice_test.go` 标记为 `//go:build cgo`，仅在启用 cgo 时启用 AST 分析能力。
2) **无 cgo 的降级实现**：
   - 新增 `pkg/static/jsluice_nocgo.go`（`//go:build !cgo`），提供与 cgo 版本兼容的类型与方法，内部用字符串/正则提取 URL，保证交叉编译可通过。
3) **build.sh 平台快捷参数**：
   - `scripts/build.sh` 支持 `./scripts/build.sh windows|arm64|all` 以及直接传 `linux/amd64,windows/amd64` 的简写；保留原 `-p/-h` 行为。
4) **文档更新**：
   - `README.md` 的“多平台构建”章节更新为包含快捷参数示例。

## 验证
- 单元测试：`go test ./...` 通过（cgo 环境）。
- 多平台构建验证（交叉编译）：
  - `./scripts/build.sh windows -o /tmp/...` 成功产物 `argo-windows-amd64.exe`
  - `./scripts/build.sh arm64 -o /tmp/...` 成功产物 `argo-linux-arm64`
  - `./scripts/build.sh all -o /tmp/...` 全平台产物生成成功

## 变更文件清单
- `scripts/build.sh`
- `pkg/static/jsluice.go`
- `pkg/static/jsluice_test.go`
- `pkg/static/jsluice_nocgo.go`
- `README.md`

## 回滚思路
- 若需回滚：`git revert` 本次提交即可恢复旧行为；或单独回滚 `pkg/static/jsluice_nocgo.go` 与 `//go:build` 标签（会重新引入交叉编译失败问题）。

