# 2025-11-14 降级记录（Engine 重构）
- 触发原因：重构 pkg/engine 下多文件需要同时删除/调整大量顶层变量与函数，Serena 的符号级编辑无法方便地批量删除这些声明，因此降级使用 `apply_patch` 编辑 normalize/result/tab/engine 及 cmd/argo。
- 操作范围：`pkg/engine/normalize.go`, `pkg/engine/result.go`, `pkg/engine/tab.go`, `pkg/engine/engine.go`, `cmd/argo.go` 进行结构性改写，并运行 gofmt/build/test 验证。
- 回滚思路：如需还原，可使用 git 查看上述文件的 diff 并逐步回退；未来若 Serena 支持删除符号，可直接用其工具重新编辑。