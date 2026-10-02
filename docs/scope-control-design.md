# Scope 正则化 + 域外结果输出 设计方案

## 背景与目标

对标 katana 的 scope 体系（/tmp/katana_src `pkg/utils/scope/scope.go`：field scope、正则、
`-do` 输出域外发现）。Argo 目前是散落三处的 `strings.Contains` 子串判断，既不精确也有安全隐患。

## 现状分析（问题清单）

1. **子串误匹配**：`pkg/engine/tab.go:343`、`tab.go:394`、`engine.go:356`、`engine.go:419` 都是
   `strings.Contains(uif.Url, ei.Host)`。目标 `a.com` 会匹配 `http://evil-a.com/`、`http://x.a.com.evil.io/`，
   把外部 URL 当域内访问；反过来想爬子域 `dev.a.com` 又进不来。
2. **域外 URL 静默丢弃**：无记录，攻击面信息（页面里引用的外部资产）直接丢失。
3. **无正则/排除能力**：想排除 `*/logout*` 之类路径只能靠 auto.filter（只管事件触发，不管访问）。

## 设计

### 新包 `pkg/scope`

```
pkg/scope/
  scope.go   规则构建与判定
```

```go
type Scope struct {
    targetHost     string            // host 或 host:port
    crawlSubdomain bool
    includeRes     []*regexp.Regexp  // 显式包含（命中即入域）
    excludeRes     []*regexp.Regexp  // 显式排除（命中即弃，优先级最高）
}

func InitFromTarget(target string, cfg conf.ScopeConf) error
func IsInScope(rawURL string) bool   // 统一入口，非法 URL 返回 false
```

**判定顺序**：exclude 命中 → false；include 非空且命中 → true；
默认规则 = `host == targetHost`（crawlSubdomain 时 `host == targetHost || strings.HasSuffix(host, "."+targetHostName)`）。
端口默认宽松（同 host 任意端口入域，与现状一致；需要收紧走 include 正则）。

替换四处子串判断为 `scope.IsInScope(u)`：
`tab.go:343`（TabWork）、`tab.go:394`（PendUrlWork）、`engine.go:356`（hijack 域内判断）、
`engine.go:419`（结果入队判断）。

### 域外结果收集

- `scope.RecordOutScope(u)`：dedup 后进 `outScopeList`（锁保护，模式同 ResultList）。
  在上述四个替换点，域外 URL 先 Record 再丢弃。
- `--outscope` 开关开启时，`SaveResult` 额外输出 `<saveName>.outscope.txt`（每行一个 URL），
  并日志汇总条数。默认关闭，保持现有输出文件集合不变。

### 配置与命令行

```yaml
scope:
  crawl_subdomains: false
  include: []    # 正则列表，如 ['^https?://[a-z]+\.target\.com']
  exclude: []    # 如 ['/logout', '/reset']
  save_outscope: false
```

CLI：`--crawlsub`（bool）、`--scope`（StringSlice，include 正则）、`--scopeexclude`（StringSlice）、
`--outscope`（bool）。

## 影响范围

- 新增 `pkg/scope`（1 文件 + 测试）。
- 修改 `pkg/engine/{tab,engine,result}.go` 判断点、`pkg/conf/conf.go`、`cmd/argo.go`、README。
- **行为变化**：子串误匹配的外部 URL 不再被访问（修复而非回归）；`evil-a.com` 类靶场外噪声下降。

## 风险

1. **默认规则收紧导致漏爬**：从 Contains 收紧为 host 精确匹配，原本「误进」的域外 URL 不再访问——
   这是预期修复；用靶场回归确认 URL 检出数不降。
2. **正则写错**：InitFromTarget 编译失败时 Fatal 提示具体哪条正则，不静默忽略。

## 子任务

- [x] `pkg/scope` + 单测（精确匹配/子域/端口/include/exclude/非法 URL/evil 前缀不匹配）(go test ./pkg/scope/ 通过)
- [x] 四处判断点替换 + RecordOutScope (tab.go×2、engine.go×2 全部改走 scope.IsInScope)
- [x] result 输出 outscope 文件 (pikachu 实测输出 261 条域外 URL)
- [x] conf/cmd 参数贯通 + README (--crawlsub/--scope/--scopeexclude/--outscope)
- [x] 靶场回归：检出数不降（pikachu 136 条与 HEAD 基线 diff 为空）、域外 URL 未开 tab（进 outscope 文件而非结果）
