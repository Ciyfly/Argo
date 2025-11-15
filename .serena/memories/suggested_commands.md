# 常用命令
- 构建：`go build -ldflags "-X main.Version=dev" -o argo cmd/argo.go` 或运行 `bash build/build.sh`。
- 单元测试：`go test ./pkg/engine/... -v`（build/test.sh 封装）。
- 示例运行：`./argo -t http://testphp.vulnweb.com/ --format txt,json`。
- 带登录脚本：`./argo -t http://192.168.192.128:8080/ --playback headless/dvwa.yml --format txt`。
- 多目标文件：`./argo -f targets.txt --format txt`（targets.txt 每行一个 URL）。