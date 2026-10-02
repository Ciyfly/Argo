# JS 接口提取 + 密钥泄漏检测 设计方案

## 背景与目标

对标 katana 的 `ScrapeJSResponses` / `Secrets` 能力（来源：/tmp/katana_src `pkg/knowledgebase/extractors/{endpoints,secrets}`，
https://github.com/projectdiscovery/katana）。

Argo 通过 HijackRequests 已经能拿到全部流量（含 JS 文件响应体），但这些内容目前只做去重存储，
没有二次情报提取。目标：

1. **接口提取**：从 JS 响应体（及内联 script）中提取 API 路径/URL，推入爬取队列，让 JS 里的隐藏接口被真实访问到。
2. **密钥检测**：从文本响应体中检测常见密钥泄漏（AWS/GitHub/Slack/私钥/JWT 等），单独输出结果文件。

## 现状分析

- 流量劫持点：`pkg/engine/engine.go:339-426` `Start()` 里的 hijack 回调，
  `ctx.Response.Payload().Body` 在 `--norrs` 时也拿得到（body 编码存储才受 `NoReqRspStr` 控制，见 engine.go:414-418）。
- 已有 URL 正则：`pkg/static/regex.go:8` `findUrlMatch` 只匹配绝对/WWW URL，不覆盖相对路径 API（`/api/v1/user`）。
- 无任何 secrets 检测能力。
- go.mod 为 Go 1.18，katana 的 titus（praetorian-inc/titus，wazero WASM 引擎）依赖高版本 Go 且体积大，
  **不引入**，改为自研规则表（见下，符合通用优先原则：规则走配置可扩展，默认覆盖常见格式）。

## 设计

### 新包 `pkg/extract`

```
pkg/extract/
  endpoints.go   接口提取
  secrets.go     密钥规则与扫描
  extract.go     入口 Init/IsJSResponse 等
```

#### 1. 接口提取 `endpoints.go`

`ExtractAPIPaths(content, baseURL string) []string`，纯通用规则：

| 规则 | 说明 |
| --- | --- |
| 绝对 URL | 复用 `findUrlMatch` 的正则（静态已有） |
| 相对路径 | `["'/`](/[\w\-.]+)+(\.(php\w?|asp\w?|jsp|do|action|json|axd|hta))?["'/]` 这类引号/斜杠包裹的路径 |
| fetch/axios 调用 | `(fetch\|axios\w*\|\$\.(get\|post\|put\|delete\|ajax))\s*\(\s*['"`]([^'"`]+)` 提取第一个参数 |
| API 风格路径 | `["']/api/v?\d*[^'"`]*["']` |

提取结果经 `url.Parse` + 相对解析（`baseURL` 为 JS 文件自身 URL）转绝对，
沿用现有 `PushUrlQueue`（SourceType `"js extract"`，Depth 0）走完整管线：
静态过滤 → 泛化去重 → scope 过滤 → tab 访问。**不直接注入结果**，访问后自然产出结果。

#### 2. 密钥检测 `secrets.go`

`SecretRule{Name, Regex, Severity}` 列表，默认规则（通用格式驱动，非站点特解）：

- AWS AccessKey `AKIA[0-9A-Z]{16}`、AWS Secret `(?i)aws(.{0,20})?['"][0-9a-zA-Z/+]{40}['"]`
- GitHub token `gh[pousr]_[A-Za-z0-9]{36,255}`
- Slack `xox[abprs]-[0-9A-Za-z-]{10,}`
- Google API key `AIza[0-9A-Za-z\-_]{35}`
- Stripe `sk_live_[0-9a-zA-Z]{24,}` / `pk_live_...`
- 阿里云 `LTAI[0-9A-Za-z]{12,20}`
- 私钥块 `-----BEGIN (RSA\|EC\|OPENSSH\|DSA)? ?PRIVATE KEY-----`
- JWT `eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`
- 邮箱+密码组合 `(?i)(password\|passwd\|pwd)\s*[:=]\s*['"][^'"]{6,}['"]`

规则可经 `config.yml` 的 `extract.secret_rules: [{name, regex, severity}]` 追加/覆盖。

`ScanSecrets(body []byte) []SecretFinding`，`SecretFinding{URL, Rule, MaskedValue, Severity}`。
匹配值打码展示（前 6 后 4，中间 `***`），完整值不落日志（安全准则）。

#### 3. 挂载点（engine.go hijack 回调内，LoadResponse 之后）

```
body := ctx.Response.Payload().Body
if IsJSResponse(req) || strings.HasSuffix(path, ".js") {   // content-type 或后缀判断
    for _, u := range extract.ExtractAPIPaths(string(body), reqURL) { PushUrlQueue(...) }
}
if extract.EnableSecrets && IsTextResponse(resp) && len(body) < maxScanBodySize {
    findings := extract.ScanSecrets(body)  // 内部去重（rule+value 维度）
    engine.CollectSecret(URL, findings)
}
```

- `IsTextResponse`：Content-Type 为 text/*、json、javascript、xml。
- `maxScanBodySize` 常量 2MB，超大响应跳过。
- 挂载在 hijack 协程内同步执行（正则毫秒级），不引入新协程，避免时序复杂度。

### 结果输出（result.go）

- 新增 `SecretList`（与 `ResultList` 同样的锁保护模式）与 `SaveSecretResult()`：
  输出 `<saveName>.secrets.txt`（`[rule] masked url` 每行）与 `.secrets.json`（完整结构）。
- 控制台 `log.Logger.Warnf` 打印每条命中（安全场景即时可见）。
- `--quiet` 模式：JSON 行输出到 stdout，与现有 URL 输出一致。

### 配置与命令行

```yaml
extract:
  enable: true        # 接口提取总开关
  secrets: true       # 密钥检测开关
  secret_rules: []    # 追加规则 [{name, regex, severity}]
```

CLI：`--extract` / `--secrets`（bool，默认 true，显式传 false 关闭——与现有 merge 规则一致以显式传参为准）。

## 影响范围

- 新增 `pkg/extract`（3 文件 + 测试）。
- 修改 `pkg/engine/engine.go`（hijack 回调 ~15 行）、`pkg/engine/result.go`（~60 行）、
  `pkg/conf/conf.go`（ExtractConf + 默认 yaml + MergeArgs）、`cmd/argo.go`（2 个 flag）。
- 不改变默认行为路径之外任何去重/调度逻辑；提取出的 URL 全部走现有管线。

## 风险

1. **正则误报**（把静态字符串当 API）：走完整管线访问，404/相似度去重自然过滤，代价是多开 tab。
   用靶场回归对比检出/耗时确认无劣化。
2. **响应体只缓存最近内容**：rod Payload().Body 对超大响应可能截断——2MB 上限 + 溢出跳过。
3. **secrets 误报**（文档/示例代码中的假密钥）：打码输出 + 规则可配置关闭，不自动验证（katana 的
   validate 会向厂商发真实请求，涉及隐私与外联，不做）。

## 子任务

- [x] `pkg/extract/endpoints.go` + 单测（正常 JS/空/超大/无 API）(go test ./pkg/extract/ 通过)
- [x] `pkg/extract/secrets.go` + 单测（各规则命中/不命中/打码）(go test ./pkg/extract/ 通过)
- [x] engine hijack 挂载 + IsJSResponse/IsTextResponse 判断 (本地靶场验证：JS 文件与内联 fetch 均以 sourceType "js extract" 入队)
- [x] result.go secrets 收集与双格式输出 (本地构造泄漏页面，.secrets.txt/.secrets.json 生成且值已打码)
- [x] conf/cmd 参数贯通 + README 更新 (--extract/--secrets flag、config extract 段、README 用法章节)
- [x] 靶场回归（pikachu 本地靶场：URL 检出与 HEAD 基线完全一致（136 条 diff 为空）；secrets 无误报）
- [x] 语义修正：接口「解析即发现」直接记结果，仅文档类地址开 tab (crawl_range 基线实测首版把 /api/* 全量入队会烧爆浏览器预算、classic 跌到 0.383；修正后 0.564、spa 0.679，详见 docs/crawl-range-baseline.md)
