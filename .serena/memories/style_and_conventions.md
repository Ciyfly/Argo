# 风格与约定
- 源码使用 Go 标准风格，默认 gofmt/golangci-lint；日志统一通过 `pkg/log` 封装的 logrus，调试日志大量使用中文描述，终端输出彩色等级。
- 配置优先 CLI flag，随后合并 `configs/config.yml`，再落在 `conf.GlobalConfig`；新增参数需同步 CLI flag 与 YAML。
- 文档、注释与对外沟通要求中文；新文件需 UTF-8、无 BOM。
- 交互脚本（headless YAML）和注入 JS 通过 `pkg/inject` embed 的 before/after 目录加载，命名采用小写加功能描述。
- Engine 层通过 channel/WaitGroup 控制并发；全局状态（`conf.GlobalConfig`、`ResultList` 等）在同进程内视为唯一实例，修改需注意线程安全。