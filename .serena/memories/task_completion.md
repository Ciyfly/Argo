# 交付检查
1. 代码格式化：`go fmt ./...` 并确保新增 CLI flag/配置有文档说明。
2. 单测：至少运行 `go test ./pkg/engine/... -v`，如修改其他包需扩展覆盖范围。
3. 构建：执行 `go build -ldflags "-X main.Version=dev" -o argo cmd/argo.go` 验证可执行文件可用。
4. 冒烟：对常见目标运行 `./argo -t http://testphp.vulnweb.com/ --format txt`；如涉及登录流程，复测 `--playback headless/dvwa.yml`。
5. 结果审查：检查 `result/<hostname>/` 下多种格式输出是否生成且 URL 去重逻辑无异常。