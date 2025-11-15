# 2025-11-13 降级记录
- 触发原因：Serena `search_for_pattern` 在读取 README.md/go.mod 等非符号文件时输出被截断，无法获取完整内容。
- 操作：在 /root/Argo 中使用 `bash -lc 'sed -n ...'` 与 `cat` 直接查看 README.md、README_EN.md、go.mod、pkg/engine/*.go 等文档，均为只读访问。
- 影响范围：仅限文件内容读取，无写入/执行副作用。
- 回滚思路：若后续 Serena 支持整文件读取，可改用 `search_for_pattern` 或新增的 read API 重复上述查询以保持留痕。