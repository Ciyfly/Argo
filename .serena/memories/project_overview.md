# 项目概览
- 目标：Argo 是一个基于 go-rod 的自动化通用爬虫，聚焦于通过无头浏览器触发页面事件、捕获网络流量并去重后导出 URL（参见 README.md）。
- 技术栈：Go 1.18、github.com/go-rod/rod 控制浏览器，urfave/cli/v2 提供 CLI，logrus 输出日志，tealeg/xlsx/json/txt/html 负责结果导出，yaml.v2/v3 读写脚本与配置。
- 结构：`cmd/argo.go` 为入口 CLI；`pkg/conf` 载入 YAML+CLI 参数；`pkg/engine` 调度浏览器、URL 队列、泛化去重和结果输出；`pkg/static` 解析 HTML/JS/robots/sitemap；`pkg/inject`/`pkg/login`/`pkg/playback` 负责页面交互与脚本回放；`pkg/vector` 做 404 判定；`pkg/updateself` 自动升级；`configs/` 默认配置；`headless/` 存放 YAML 脚本。
- 运行形态：通过 `./argo -t <target>` 启动一个 Engine，内部使用 Tab 池与 HijackRequests 抓取 URL 并写入 result 目录。