# 表单字段级填充规则 设计方案

## 背景与目标

对标 crawlergo 的 form config / katana 的 FormConfig：按字段 name/id/正则配置填充值。
Argo 现在只有固定默认值（username/password/email/phone），真实站点常见字段
（搜索框、验证码、日期、身份证、金额）填不对导致表单流程断链。

## 现状分析

- `pkg/inject/auto.go` autoFillJS：input/textarea 按 type 固定四值；checkbox/radio 勾选（本轮已加）。
- submitFormsJS：类似固定值 + number 填 20。
- 无 skip 机制：验证码字段填了假值反而可能把表单卡在校验失败。

## 设计

### 配置（config.yml）

```yaml
form:
  rules:                       # 用户规则，按序优先匹配
    - match: "cardno|card_no"  # 正则，对 name/id/placeholder/aria-label 拼串小写匹配
      value: "110101199001011234"
    - match: "^amount$"
      value: "100"
  skip: ["captcha|verify_code|验证码|otp"]   # 命中即不填（验证码类默认跳过）
```

CLI 不加规则参数（规则天然属于配置文件，避免命令行转义地狱）。

### 填充优先级（autoFillJS / submitFormsJS 统一逻辑）

```
1. skip 规则命中        → 不填，跳过该字段
2. 用户 rules 命中      → 填 value
3. 内置语义规则（按 type + 关键词表）：
   search → "argo"、email → 默认邮箱、tel → 默认手机、url → https://example.com、
   number/range → 20、date → 2020-01-01、password → 默认密码、text → 默认用户名
4. 现行为兜底
```

内置关键词表（中英文通用，可被 skip/rules 覆盖）：搜索/search/查询 → "argo"；
验证码/captcha/verify/otp/短信 → 默认 skip（爬虫不该猜验证码）。

### 传递方式

rules/skip 由 Go 编译（正则非法在启动时 Fatal，提示哪条错）后序列化为 JSON 字符串，
经 `page.Eval(autoFillJS, username, password, email, phone, rulesJSON)` 传入，
JS 内 `JSON.parse` 后按上述优先级执行。两个 JS 函数共用同一段匹配逻辑。

## 影响范围

- `pkg/conf/conf.go`（FormConf + 默认 yaml）
- `pkg/inject/auto.go`（两个 JS 函数 + Auto 调用点）
- `pkg/engine/engine.go`（启动时编译规则，非法正则 Fatal）

## 风险

1. **正则写错**：启动即校验 Fatal，带规则内容报错。
2. **JS 复杂度**：匹配逻辑 ~30 行，与现有 setValue 结构一致，无外部状态。

## 子任务

- [x] FormConf + 正则编译校验 (formRulesJSON + FormRulesCheck 启动 Fatal)
- [x] autoFillJS/submitFormsJS 规则化改造 (formFieldMatcherSrc 公共片段，skip>用户规则>内置语义>兜底)
- [x] 单测 (inject 包全绿)
- [~] 靶场验证 (classic strict 表单命中依赖具体校验规则，headless 回归 0.946 未退步；字段级规则对 strict 的增益待带规则配置复测)
