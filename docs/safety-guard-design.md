# 危险操作防护（safety guard）设计

状态：已完成（2026-10-11，DVWA 端到端 + classic 零回归验证，见 §7）
日期：2026-10-11
关联：docs/form-fill-design.md（表单填充规则）、docs/scope-control-design.md（scope exclude）

## 1. 背景与目标

### 1.1 实测问题（2026-10-11，本地 DVWA，带登录态）

带登录 Cookie 爬 `http://127.0.0.1:13001/`（DVWA，security=low），Apache 访问日志与数据库证实 Argo 默认行为会**修改目标状态、销毁自己的会话**：

```
GET /vulnerabilities/csrf/?password_new=argo123&password_conf=argo123&Change=Change   ← 改密码为 argo123
GET /vulnerabilities/csrf/?password_new=&password_conf=&Change=Change                 ← 又改成空密码
GET /logout.php 302                                                                   ← 会话被登出
POST /security.php 302 / GET /security.php?phpids=on                                  ← 改了安全设置
```

爬完后 `dvwa.users.admin` 哈希 = `d41d8cd9...` = MD5("")，密码被改成**空**。

### 1.2 根因（代码定位）

| # | 根因 | 位置 |
| --- | --- | --- |
| R1 | `submitFormsJS` 把页面上**每个带 action 的表单**都真实提交，仅有硬编码的 logout/signout 跳过，没有密码修改/删除/重置类黑名单 | `pkg/inject/auto.go:227-261`（跳过逻辑在 230-232） |
| R2 | `clickBySigJS` 只把 `<button type=submit>` 降级为 type=button，**漏了 `<input type=submit>`**——点击循环点到它时表单以 DOM 当前值真实提交（空密码提交即此路径：点击时页面是重新加载过的，输入框为空） | `pkg/inject/auto.go:468-471` |
| R3 | 菜单里的 `Logout` 链接被静态解析/scanLinksJS 当普通 URL 收集并入队开 tab，直接 GET `logout.php` 杀掉会话。`scope.exclude` 默认 `[]`，`auto.filter` 只管点击层、管不到 URL 入队 | `pkg/inject/auto.go:108-135`（收集）、`pkg/engine/tab.go:536-593`（PendUrlWork 消费）、`pkg/conf/conf.go:60`（exclude 默认空） |
| R4 | `auto.filter` 默认值拼写错误：`lougout` ≠ `logout`，连点击层的 logout 跳过也从未生效 | `pkg/conf/conf.go:53` |

### 1.3 目标

- **默认安全**：不改密码、不提交删除/重置类表单、不访问登出链接——不做任何配置时即生效
- **URL 发现不受损**：`logout.php` 这类 URL 仍进结果（爬虫本职是发现 URL），只是**不打开**
- **可配置、可关闭**：`safety.enabled: false` 一键恢复旧行为；黑名单可整体替换
- **通用机制**：按协议/字段名/URL 结构匹配，无任何 DVWA 特定逻辑（符合全局准则 4.1）

### 1.4 非目标

- 不做 JS 静态分析判断表单语义（正则对 outerHTML/URL 匹配已覆盖实测场景）
- 不拦截 GET 参数型 URL 的一切访问（只拦登出/会话销毁类；其余状态变更 URL 无法从 URL 本身可靠识别，属扫描器通用边界）
- `security.php` 这类**会话级设置表单**（DVWA 安全级别只存 session）不进默认黑名单——关键词过宽会误伤真实站点，记为已知残余风险（见 §6）

## 2. 现状分析：现有防护与缺口

| 层 | 现有机制 | 缺口 |
| --- | --- | --- |
| 点击层 | `auto.filter`（outerHTML 含关键词则 skip）；`<a href>`/`formaction` 点击前摘除；`BUTTON[type=submit]` 降级 | R4 拼写错误；R2 `<input type=submit>` 未降级 |
| 填充层 | `form.skip` 默认跳过验证码字段 | 只管填充不管提交 |
| 提交层 | 硬编码跳过 action/innerHTML 含 logout/signout 的表单（`auto.go:232`） | 硬编码不可配、黑名单只有登出一类（R1） |
| URL 层 | `scope.exclude` 正则（命中=出域丢弃，含 `save_outscope` 输出）；scope 判断在 TabWork/PendUrlWork/hijack 共 4 处 | 默认空，无安全默认值；exclude 语义是"丢弃"，不适合承载"记录但不访问" |
| 结果通道 | `pushpendingNormalizeQueue`（`pkg/engine/normalize.go:64`）已用于内容类型预检/hijack 的「发现未开 tab」记录 | safety 可直接复用该通道 |

## 3. 设计

### 3.1 配置结构（`pkg/conf/conf.go`）

新增顶层 `safety` 组，写进 `defaultYamlConfigStr`（conf.go:21-68 的默认模板）：

```yaml
safety:
  enabled: true           # 总开关；false 恢复旧行为（全提交全点击）
  auto_profile: true      # 站点指纹识别（见 §3.6）：命中 DVWA 等已知站点自动叠加 strict 策略
  profile: "default"      # default | strict（strict 额外拦设置类表单，见 §3.6）
  # 不打开的 URL（正则，对完整 URL 小写匹配）。命中仍记录为「发现未请求」结果
  url_skip: ["logout|signout|sign-out"]
  # 不自动提交的表单（正则，对 form outerHTML 小写匹配；outerHTML 已含 action/字段name/按钮文案）
  # 注意：不放裸 "reset"/"清空" —— 登录表单里常有「重置密码」链接/清空按钮，会误伤智能登录
  form_submit_skip:
    - "logout|signout|sign-out|注销|登出"
    - "password_new|password_conf|password_current|old_?pass(word)?|new_?pass(word)?|curr_?pass(word)?|修改密码|更改密码|重置密码"
    - "delete_?(user|account)|remove_?(user|account)|删除用户|删除账号"
```

- 结构体：`type SafetyConf struct { Enabled bool; UrlSkip []string; FormSubmitSkip []string }`，挂到 `Conf`（conf.go:70 附近）
- **enabled=false 或列表为空时完全跳过**，不注入、不匹配，零行为差异
- 不加 CLI flag：与 `auto.filter` 同类（纯策略配置），走配置文件；避免 MergeArgs「命令行默认值覆盖配置文件」的坑

### 3.2 表单提交黑名单（修 R1，`pkg/inject/auto.go`）

- 现有硬编码（auto.go:230-232 的 `low.indexOf("logout")`）删除，改为读取 `safety.form_submit_skip` 正则列表
- 注入方式复用 `formRulesJSON`（auto.go:267-287）模式：Go 侧 `safetyRulesJSON()` 序列化 + **启动时编译校验正则**（非法 Fatal，与 form.rules 同路径），`submitFormsJS` 增加一个参数接收
- JS 侧判定（在现有 logout 检查位置替换）：

```js
const lowHTML = (f.outerHTML || "").toLowerCase();
for (const re of submitSkipRes) {
    try { if (new RegExp(re).test(lowHTML)) { skipped.push(f.getAttribute("action") || "?"); return; } }
    catch (e) {}
}
```

- 返回值附加 `skipped` 列表，Go 侧 `log.Logger.Debugf("safety skip form: %v", ...)`，debug 可见、不进结果

### 3.3 危险表单点击层拦截（修 R2，`auto.go` clickBySigJS）

> 实现与初稿有偏差：初稿打算把 `<input type=submit>` 降级为 type=button。
> 实测 classic 基准暴露问题——shop 的筛选/排序/分页依赖「改 select → 点
> input[type=submit] 原生提交表单」，无差别降级会误杀。且降级只保护点击路径，
> 与 submitFormsJS 的表单级黑名单语义割裂。
>
> 最终实现：**按「表单是否危险」判断**——点击目标是 `input[type=submit]` 且
> 所属表单命中提交黑名单（`formIsDangerous(el.closest("form"))`）时整个不点；
> 非危险表单保留原生提交（检出率不受影响）。危险表单在 submitFormsJS 与
> 点击层两条路径都被拦截，语义一致。

### 3.4 URL 安全过滤（修 R3，`pkg/engine/tab.go`）

- 收口点：**`PendUrlWork`**（tab.go:536）。验证过的数据流：所有 URL（首页 engine.go:565 / 静态解析 / auto hrefList / fuzz / metadata）都经 `PushUrlQueue` → `UrlsQueue` → `PendUrlWork`，之后才可能 `PushTabQueue` 开 tab——在此一处过滤即全覆盖
- 位置：scope 检查（tab.go:550）之后、`filterStaticPendUrl` 之前：

```go
// safety：登出/会话销毁类 URL 不打开，仍记录为「发现未请求」结果
if safety.ShouldSkipURL(uif.Url) {
    pushpendingNormalizeQueue(&PendingUrl{URL: uif.Url, Method: "GET", Status: 0, ...})
    continue
}
```

- 判定函数放 `pkg/safety/safety.go`（新包，避免 scope/result 职责膨胀）：

```go
func ShouldSkipURL(rawURL string) bool  // enabled=false 直接 false；正则对 strings.ToLower(rawURL) 匹配
func ShouldSkipFormHTML(html string) bool // JS 侧不用它，但供单测对齐语义
```

- 与 `scope.exclude` 的语义区分（文档明示）：
  - `safety.url_skip`：默认开、**结果保留**（记为未请求）、可关闭——安全网
  - `scope.exclude`：默认空、结果丢弃（可选 outscope 输出）——用户主动划界

### 3.5 auto.filter 拼写修复（修 R4，`conf.go:53`）

```yaml
filter: ["logout", "登出", "reset"]   # lougout → logout
```

- 仅默认模板一处；`AutoConf.Filter` 无 CLI flag（已确认 cmd/argo.go 无此参数）

### 3.6 站点指纹识别 + strict 策略档案（用户决策，2026-10-11）

用户拍板：**设置类表单（security.php 等）在 DVWA 上拦、在真实站点不拦**。落法是「指纹识别 → 叠加策略档案」，而非硬编码 if 靶场：

- **识别**：`safety.ObserveHTML(html)` 在 NewTab 拿到页面 HTML 时调用（`pkg/engine/tab.go:232` 附近），指纹命中即置 strict（进程内一次性，sync/atomic）
- **DVWA 指纹**（大小写不敏感子串）：`damn vulnerable web application`、`>dvwa<`（title/页脚均含；"dvwa" 裸子串误报率高，不加）
- **strict 档案效果**：表单提交黑名单追加内置设置类列表 `["security|phpids|安全设置"]`；URL 过滤不变
- **配置**：

```yaml
safety:
  auto_profile: true   # 识别到已知站点指纹时自动叠加该站点的 strict 策略
  profile: "default"   # default | strict（手动强制 strict，不依赖指纹）
```

- **符合 4.1 第 3 层特解规范的说明**：为何通用不够——"security/settings" 在真实站点过宽（安全提问、安全设置很常见），默认拦会误伤普通表单，故降为「指纹触发 + 可手动开启」；触发条件——页面 HTML 含 DVWA 指纹或 `profile: strict`；如何关闭——`auto_profile: false` + `profile: default`。指纹列表后续按同机制扩展（pikachu 等），不散落核心路径

### 3.7 DVWA 默认行为对照（设计后预期）

| 危险操作 | 设计后行为 |
| --- | --- |
| CSRF 表单提交（改密码） | `form_submit_skip` 命中 `password_new` → 表单不提交；点击层 input[type=submit] 降级 → 不再空值提交 |
| `Logout` 链接 | URL 照常出现在结果里（未请求），不开 tab |
| `security.php` 设置表单 | DVWA 指纹命中 → strict 追加列表命中 `security` → 不提交；真实站点无指纹 → 行为不变 |

## 4. 影响范围

| 文件 | 改动 |
| --- | --- |
| `pkg/conf/conf.go` | +`SafetyConf` 结构体；`defaultYamlConfigStr` 增 safety 段 + 修 filter 拼写；`LoadConfig` 后初始化 safety 正则（非法 Fatal） |
| `pkg/safety/safety.go`（新增） | `Init`/`ShouldSkipURL`/`FormSkipPayload`；单测 `safety_test.go` |
| `pkg/inject/auto.go` | `submitFormsJS` 黑名单参数化（替换硬编码）；`clickBySigJS` INPUT submit 降级 |
| `pkg/engine/tab.go` | `PendUrlWork` 加 safety URL 过滤（~6 行） |
| `docs/TODO.md` | 新增需求条目 |

不改：`listClickableJS`/`dispatchEventsJS`/登录匹配（match.go，仅登录页生效）/`scope` 包语义/hybrid fetcher（纯 GET）。

## 5. 测试计划

1. **单测**（`pkg/safety/safety_test.go`）：
   - `ShouldSkipURL`：logout.php/signout 命中；正常 URL 不命中；enabled=false 全不命中；非法正则启动 Fatal（conf 层测试）
   - `ObserveHTML`：DVWA 首页/登录页 HTML 命中指纹 → strict；普通站点 HTML 不命中；`auto_profile=false` 不命中；`FormSkipPayload` 在 default/strict/disabled 三态下内容正确
2. **DVWA 端到端回归**（真实验证，验收标准）：
   - 带登录态爬 `127.0.0.1:13001`，Apache 日志中**不得**出现：`password_new`、`GET /logout.php`、`Change=Change`
   - 结果文件中**必须**包含 `logout.php`（发现未请求）
   - 爬完数据库 `admin` 哈希仍为 `5f4dcc3b...`
3. **检出率回归**：classic 基准站 `192.168.0.130:8765` 检出率不低于 0.946（确认黑名单不误伤普通表单）；`safety.enabled=false` 时与旧行为 diff 为零

## 6. 风险与权衡

| 风险 | 评估 | 缓解 |
| --- | --- | --- |
| 黑名单误伤普通表单（如搜索表单带 `delete_flag` 字段） | 低：默认列表词都偏专有，且刻意不放裸 `reset`/`清空`（会误伤带「重置密码」链接的登录表单） | 列表整体可替换（配置即全量覆盖，非追加）；`enabled` 可关 |
| 设置类表单（security.php）在真实站点仍会被提交 | 中：真实站点上属用户可感知的状态变更 | 用户拍板不默认拦（§3.6）；DVWA 类站点指纹自动拦，真实站点可 `profile: strict` 手动开 |
| 登出变体没拦全（如 `/user/exit`、POST 型登出） | 低：登出被点到的后果只是会话丢失（爬虫自身受损，非目标受损） | 用户可往 `url_skip` 追加；不追求穷举 |
| INPUT submit 降级影响依赖按钮点击提交的 SPA 表单 | 低：真实提交统一走 submitFormsJS（requestSubmit），不依赖 click 原生提交 | 检出率回归（§5.3）把关 |

## 7. 子任务与验证记录

- [x] conf：SafetyConf 结构体 + 默认 yaml 模板 + filter 拼写修复（验证：单测+老配置无 safety 段时默认生效）
- [x] pkg/safety 新包：ShouldSkipURL / FormSkipPayload / ObserveHTML 指纹识别（§3.6）（验证：`go test ./pkg/safety/` 5 个用例全过）
- [x] auto.go：submitFormsJS 黑名单参数化（删硬编码 logout 检查，返回 skipped 供 debug 日志）
- [x] auto.go：clickBySigJS 危险表单点击层拦截（§3.3 实现偏差说明）
- [x] tab.go：PendUrlWork 接入 ShouldSkipURL（命中走 pushpendingNormalizeQueue「发现未请求」通道）；NewTab 接入 ObserveHTML
- [x] DVWA 端到端回归（验证 2026-10-11 09:15 run：Apache 日志零 `password_new`/`Change=Change`/`GET /logout.php`/`POST /security`；admin 哈希保持 5f4dcc3b；结果含 logout.php（未请求）；97 tab/189 结果覆盖登录后页面；strict 由指纹自动触发并打日志）
- [x] classic 基准回归（验证 2026-10-11：safety 版 0.745 与 HEAD 版 0.745 的 missing 集合**完全一致**，零回归）
- [x] README 功能与配置说明同步

### 7.1 排查记录：classic "0.946→0.745" 系靶场容器回退，非代码回归

- 现象：safety 版与 HEAD 版 classic 同为 0.745，shop/pagination/shadow/form-strict/xhr 缺口集合完全一致
- 二分：当年测出 0.946 的 `83821ea` 二进制在今天也只跑出 0.745 → 排除代码因素
- 根因：`/classic/shop/products.html` 等基线路径 404；靶场容器 `/api/meta/stats` 自报 must_total=112，而 expected 基线为 149——**10-08 前后容器被旧镜像（7-23 构建，缺 shop/pagination/shadow/strict 套件）拉起**，而 10-03 测基线时跑的是 8-24 构建的 `crawl-range:latest`（afb98f97）
- 修复：`cd /root/code/crawl_range && docker compose up -d` 重建为 latest 镜像（must_total 恢复 149）
- 复测（2026-10-11 09:59，safety 版）：classic **0.9664**，shop/pagination 100%、shadow 0.78，高于基线 0.946（run 波动内），safety 零回归
- 备注：must_not_violations=2（evil.example、/classic/logout）为评分口径把「发现未请求」也计违规所致，HEAD 行为一致，与 safety 无关
