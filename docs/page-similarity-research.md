# 页面内容相似度去重 调研报告

状态：**调研完成，未实现**。本文给出结论与建议路线，供后续立项决策。

## 对标实现（katana，MIT）

来源：/tmp/katana_src `pkg/similarity/`、`pkg/engine/common/base.go:86-175`。

- **三种后端**：simhash（默认）/ tfidf / bm25（`pkg/similarity/index.go:10-24`）。
- **simhash**：64 位 Charikar 特征哈希 + 汉明距离 ≤3 判相似（`simhash.go`），特征是分词，
  比较 O(1)，适合大规模。
- **聚类预算（关键设计）**：`clusters map[clusterID]int`，每个内容簇默认只放行 `Budget=1` 个页面，
  其余剪枝；clusterID 来自路径归一化。防止「同模板换参数」的页面无限消耗预算。
- **另一维度**：`FilterSimilar` 用 PathTrie 按 URL 路径相似度剪枝（`base.go:86`），
  与内容相似度互补。
- 配套测试（`server_test.go`）验证数百相似页被过滤、budget 可调、关闭后全保留。

## Argo 现状

- `pkg/vector`：HTML 分词 → 词频向量 → 余弦相似度（英文 snowball 词干化）。
  目前只用于软 404 判定（`pkg/engine/tab.go:185-195`，与随机 404 页基线比，阈值 0.95）。
- 每个页面在 NewTab 里已经计算了 `currentPageVector`（tab.go:185）——**特征已现成，只缺索引与剪枝逻辑**。
- 已有泛化模式计数 `normalizePatternCount + maxURLsPerPattern=50`（`normalize.go:32-39`），
  这是「URL 维度」的防翻页，不感知页面内容：同模板不同路径仍会各开一个 tab。

## 方案对比

| 方案 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- |
| A. 复用 pkg/vector 余弦 + 簇预算 | 零新依赖；向量已计算；与 404 判定同源 | 两两比较 O(n²)（n=页面数，千级可接受）；英文词干化对中文页面弱 | **推荐** |
| B. 自实现 simhash | O(1) 比较，规模无上限 | 新增 ~80 行算法代码；Argo 页面量级（千级）用不到 | 暂不需要 |
| C. 引入第三方库（如 katana 代码移植） | 功能全 | 依赖/许可证/维护成本；Argo 用不到 tfidf/bm25 | 不做 |

### 推荐方案 A 设计要点（供立项时展开）

1. **剪枝位置**：`NewTab` 拿到 `currentPageVector` 之后、注入/auto 触发之前——
   相似页面直接关 tab，省的是交互阶段（当前耗时大头），而不是省加载。
2. **簇键**：复用 `normalizeationPattern`（现成、稳定），每簇维护已收页面向量集合 + 已收计数。
3. **判定**：与簇内任一已收向量余弦 > 阈值（默认 0.95，可配）且簇内已收数 ≥ budget（默认 3）→ 剪枝。
   即「同类页面最多认真看 budget 个」，与 katana 的 Budget 思想一致但实现更简单。
4. **配置**：`browser.similar_budget: 3`、`browser.similar_threshold: 0.95`，0 = 关闭。
5. **风险**：阈值过紧会误剪「长得像但有增量链接」的列表页——budget≥3 + 走靶场回归
   （检出数对比）再定默认值；中文站点需先验证 snowball 对中文的分词效果（当前按空白切词，
   中文整句成一个「词」，相似度会偏高）。

## 结论

- 能力缺口真实存在（同模板页面浪费 tab 预算），但优先级低于已立项的 JS 提取/限速/scope。
- 实现成本低（复用 vector + normalize，预计 ~150 行 + 测试），建议下个迭代立项。
- 若立项，先跑一次「中文站相似度分布」实验确定阈值，避免拍脑袋默认值。
