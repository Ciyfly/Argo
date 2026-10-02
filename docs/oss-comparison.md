# 开源爬虫横向对照与提升清单

数据来源：本地 crawl_range bench（2026-10-02）、katana 源码（/tmp/katana_src，MIT）、
crawlergo README（github.com/Qianlitp/crawlergo）、rad README（github.com/chaitin/rad）。
注意：crawlergo/gospider/hakrawler 的 bench 是旧版靶场上的历史跑，与 katana/argo 的新跑
不完全同口径——量级可信，精确分数不可比。

## 总分矩阵（Argo 为 2026-10-02 交互修复后实测）

| 轨道 | **Argo** | katana | crawlergo | gospider | hakrawler | katana无头 |
|---|---|---|---|---|---|---|
| classic | **0.946** | 0.899 | 0.812* | 0.302* | 0.315* | 0.322 |
| spa | **0.764** | 0.386 | 0.386* | 0.457* | 0.007* | 0.164 |
| classic_auth | 0.75** | 0.75 | 0.25* | 0.25* | 0.20* | 0.20 |
| spa_auth | 0.154 | 0.154 | **0.846*** | 0.154* | 0 | 0.077 |

\* 旧口径历史数据；\** 实测需要正确配置（-u demo 或 --cookie），未配置时 0.25。
classic/spa 检出能力 Argo 已全面第一；速度 katana 仍最快（classic 20s vs Argo 88s，http-only 引擎优势）。

## 各项目独有能力与 Argo 现状

### crawlergo（0Kee-Team）
- 自定义表单填充规则（form config 按 name/regex 配置各字段值）→ **Argo 只有固定默认值，缺字段级规则**
- 结果推送被动扫描器（xray 联动）→ Argo 无（json 全请求响应输出已具备基础）
- robots.txt + 常见路径 Fuzz → Argo 有 robots/sitemap，无路径 fuzz
- Host 绑定 / 自动补 Referer → Argo 无
- SPA 登录后动态路由跟进（spa_auth 0.846 的来源）→ **Argo 0.154，真缺口**（登录成功后
  服务端下发路由表、router.addRoute 动态注册、链接登录后才渲染——Argo 登录成功后没等/没跟进）

### rad（长亭）
- `--wait-login` 人工登录模式（开有头浏览器暂停，人工登录后回车继续爬）→ **Argo 无，高价值**
- `parent_path_detect` 父目录探测（/a/b/c → 探 /a/b/、/a/）→ Argo 无，低成本
- `domain_headers` 按域名自定义请求头（Cookie 注入的通用形式）→ Argo 有浏览器级 --cookie，
  **缺 header 级注入**
- max_page_visit 页面预算 + 爬虫陷阱防护 → Argo 有泛化 pattern 限流（maxURLsPerPattern=50），部分等价

### katana（剩余优势）
- 标准引擎速度（20s vs 88s，4.4 倍）——hybrid 引擎差距。Argo 的 ProbeContentType 分流已是种子
- 断点续爬 resume、字段级输出控制（Fields/FieldConfig）、TechDetect 指纹、
  knowledgebase ML 页面分类、resolvers——均为次要工程项

### gospider / hakrawler
Bench 全面落后，无借鉴点（gospider 的 includes/excludes 参数形态 Argo 的 scope 已覆盖）。

## 提升清单（按价值/成本排序）

1. **SPA 登录后动态路由跟进**（spa_auth 0.154→0.8+）：监听登录 XHR 成功响应（302/200+token），
   等待路由注册与导航（next 字段），登录后重新 collect——通用机制非站点特解
2. **--waitlogin 人工登录模式**（rad 同款）：有头浏览器 + 暂停等回车，覆盖验证码/短信登录
   （Argo 明确不支持验证码，这是现成的绕路方案）
3. **--header 按域名自定义请求头**：Cookie/token 注入的通用形式，auth admin 档标准姿势
4. **表单字段级填充规则**：config 按 name/id/regex 配置字段值，默认规则表覆盖常见语义
   （搜索/邮箱/手机/验证码类跳过），crawlergo/katana 同款
5. **父目录探测**：/a/b/c.html → 探测 /a/b/、/a/（rad parent_path_detect）
6. **混合引擎（速度）**：HTML 页面先 Go http 抓解析（已有 ProbeContentType 雏形），
   仅交互/JS 渲染依赖页开浏览器——目标 classic 88s → ~30s
7. 断点续爬 resume、被动扫描器推送输出、页面相似度去重（已调研）——次要
