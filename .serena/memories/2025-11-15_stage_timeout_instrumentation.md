2025-11-15：为定位 tab 超时新增阶段化记录。
- 修改 pkg/engine/tab.go，在 (*EngineInfo).NewTab 中使用 atomic.Value 保存 stage，并引入 setStage/getStage，覆盖 open_page/wait_load/page_info/html_snapshot/inject/middlewares/push_urls 等阶段，同时将 PageContext.StageRecorder 与 runPageMiddlewares 对接，支持输出 middleware:* 阶段，time.After 超时日志和 RecordTimeoutReason 会读取精确 stage。
- 修改 pkg/engine/pipeline.go（新增 StageRecorder 字段）与 pkg/engine/engine.go（runPageMiddlewares 在每个 middleware 前调用 StageRecorder）。
- 调整期间因 gofmt 前的语法错误导致 Serena 无法定位 PageContext，降级使用 apply_patch 修复 `type type PageContext`。
- go test ./... 通过（pkg/engine 等）。