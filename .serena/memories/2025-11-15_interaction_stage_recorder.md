2025-11-15：进一步精细化 interaction 阶段的超时定位。
- InteractionContext 现包含 StageRecorder，interactionMiddleware 会在执行前写入 "interaction_chain:start"，结束写入 "interaction_chain:done"，并在 Engine.runInteractions 中为 login/playback/auto 等单个交互前后写入 "interaction:<name>" 和 ":done"。
- 调整影响文件：pkg/engine/interaction.go、pkg/engine/pipeline.go；go test ./... 通过。
- 结合之前的 tab 阶段记录，日志现在能清楚区分 middleware:interaction vs interaction:auto 等子阶段。