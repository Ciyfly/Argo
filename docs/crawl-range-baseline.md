# 爬虫基线（crawl_range）对照：Argo vs Katana

数据来源：本地 `crawl-range`（/root/code/crawl_range），评分 `score/score.py` + 工作区 `expected/classic_v1.json`（must=149）。
katana 参数：标准模式 `-d 3 -jc -aff -kf all -c 10`（无浏览器）；Argo：`--maxdepth 4 --tabcount 8 --tabtimeout 15 --browsertimeout 140 --norrs`。

## ⚠️ 测量环境教训（2026-10-02 两次勘误）

1. **Docker 容器里是旧版靶场**（无 shop 路由、expected must=112），工作区代码才是新版（must=149）。
   今晨官方 bench 与首次复测都打在旧容器上、用新 expected 评分，虚增了缺口。
2. **靶场 `sitemap.xml` 硬编码 `http://127.0.0.1:8765/...` 绝对地址**：在非 8765 端口起新版靶场时，
   Argo 的 sitemap 解析会把旧容器 URL 全部吸入（一次 run 双靶场，预算腰斩）。
   复测须加 `--scopeexclude '^http://127.0.0.1:8765'` 或在容器内打。
3. 历史结论中「L4 shop 分页/筛选缺口」「部分表单缺口」是上述测量假象，**实际不存在**。

## 修正后成绩（2026-10-02 交互修复完成后终版）

| 轨道 | Argo | katana 标准模式 | katana 无头 |
| --- | --- | --- | --- |
| classic must_rate | **0.946 (141/149)** | 0.899 | 0.322 |
| spa must_rate | **0.764 (107/140)** | 0.386 | 0.164 |

**classic 与 spa 双轨均超过 katana**。修复明细见 docs/interaction-fix-design.md：
SVG 等非 HTMLElement 点击兜底、select 逐项收集、checkbox 预勾选、非文档 URL 内容类型
预检（Go 侧轻量 GET 分流，json 接口不开浏览器）、外层 tab 超时与交互预算时钟统一、
固定 tab 超时改进度驱动 idle 超时（--tabidle）、HTML 内联 script 完整提取。

## classic 剩余缺口（8 条，run 间波动 ±5）

POST 表单接口（feedback/subscribe/upload-meta/form-strict/form-upload，提交时序波动）、
download-sample.bin（点击生成的 .bin 下载链）、closed shadow DOM 2 条（closed-vault/srcdoc）。
