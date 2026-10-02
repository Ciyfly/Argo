# 字段级输出 设计方案

## 背景与目标

对标 katana 的 `Fields/FieldConfig/OmitRaw`：按需输出结果的指定字段、自定义输出模板。
Argo 现在 json/quiet 固定输出全部字段（含巨大的 base64 请求响应串），txt 固定
`[METHOD]URL` 格式——下游消费（打分、入库、喂扫描器）只要其中几列时很浪费。

## 现状分析

- `pkg/engine/result.go`：`writeResultToJson`（MarshalIndent 全字段）、`writeResultToText`
  （固定格式）、quiet 模式 JSON 行（全字段）。
- PendingUrl 字段：URL/Method/Host/Headers/Data/Status/ResponseHeaders/ResponseBody/RequestStr。

## 设计

### `--fields <csv>`（默认空 = 全字段，行为不变）

合法字段：`url,method,host,data,status,request_str,response_body`（与 PendingUrl 对应，
headers 类不单独暴露——太大，随 request_str/response_body 即可）。
json 与 quiet 输出按 `--fields` 构造有序 map，只含所选字段；非法字段名启动 Fatal 并列出合法集。

### `--outputtemplate <Go模板>`（默认空 = 现固定格式）

txt 行模板，`text/template` 语法，数据为 `*PendingUrl`：

```shell
# 只输出 method + url + status
./argo -t http://a.com/ --outputtemplate '{{.Method}} {{.Status}} {{.URL}}'
```

模板解析错误启动 Fatal。**安全**：模板由使用者本人传入（与 katana OutputTemplate 同等信任级别），
不涉及注入他人输入。

### 追加模式与多格式

`--mergedOutput` 的 txt 追加（appendTxtResult）走同一模板；json 追加按 `--fields` 过滤。
xlsx/html 保持全字段（表格/报告场景就是要全量）。

## 影响范围

- `pkg/conf/conf.go`（ResultConf.Fields/OutputTemplate）
- `cmd/argo.go`（两个 flag）
- `pkg/engine/result.go`（过滤函数 + txt 模板渲染 + quiet 分支）
- README

## 风险

1. **模板执行错误**（字段类型不匹配）：渲染时错误降级为该行输出原始 URL 并 debug 日志，不中断整个落盘。
2. **字段名拼错**：启动即校验，Fatal 提示合法字段列表。

## 子任务

- [x] fields 校验与过滤 (filterResultFields，非法名 Fatal 列合法集)
- [x] outputtemplate 渲染 (resultLine，错误降级固定格式)
- [x] quiet 与 mergedOutput 分支同步支持 (appendJson 统一 map 路径)
- [x] 冒烟验证 (s.json 只含 url/method、s.txt 模板生效；README 示例待补)
