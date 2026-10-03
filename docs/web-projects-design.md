# Web 控制台 v2：项目管理 + 多目标 + 结果导出 设计方案

## 背景与目标

v1（docs/web-console-design.md）只有临时任务。升级为**项目制**：

- 创建项目（名称 + 多个目标 URL + 参数）
- 项目内目标**顺序爬取**（全局仍是单活跃爬取——engine 全局状态约束）
- 每个目标有独立状态（待爬/爬取中/完成/停止/失败）
- 项目结果**持久化**（进程重启不丢）
- 一键**导出**：txt / json / csv

## 数据模型与持久化

```
webdata/
  projects.json          # 项目元数据索引（id、名称、目标列表与状态、参数、统计）
  <projectID>.results.json  # 单项目累积结果（[]PendingUrl 序列化）
```

```go
type Project struct {
    ID        string        `json:"id"`
    Name      string        `json:"name"`
    Targets   []TargetState `json:"targets"`   // {url, status, resultCount, error}
    Params    TaskOptions   `json:"params"`
    CreatedAt string        `json:"created_at"`
    Status    string        `json:"status"`    // idle/running/done/stopped
}
```

存储目录：二进制旁 `webdata/`（与 configs/ 同级）。写盘走 tmp+rename 原子写。

## API

| 路由 | 说明 |
| --- | --- |
| `GET/POST /api/projects` | 列表 / 创建 |
| `GET/DELETE /api/projects/{id}` | 详情（含已存结果）/ 删除 |
| `POST /api/projects/{id}/start` | 从第一个未完成目标顺序爬 |
| `POST /api/projects/{id}/stop` | 停止当前目标并中止后续 |
| `GET /api/projects/{id}/export?format=txt\|json\|csv` | 导出下载 |
| `GET /api/results`（SSE） | 直播流，事件带 `project_id`，前端过滤非当前项目 |
| `GET /api/stream` | MJPEG 画面直播（不变） |

## 运行管理（pkg/web 内置 runManager）

- 全局单活跃爬取：`active` 记录 {projectID, targetIdx}；他项目 start 被拒（409）
- runner goroutine：取项目下一个 pending 目标 → 写参数 → `engine.RunAsync` → done 后
  `engine.SnapshotResult()` 追加进项目结果并持久化 → 标记 done → 继续下一个
- stop：`engine.StopCurrent()` + 置 stopRequested → runner 在当前目标结束后不再继续
- 目标失败（浏览器起不来等）：标记 failed 继续下一个

## UI（console.html 重构）

- 左侧窄栏：项目列表 + 新建按钮（弹层：名称/多目标 textarea/参数）
- 主区选中项目：头部（名称、目标进度点、启动/停止/导出 txt·json·csv、删除）
- 主体左右分栏保持 v1：URL 实时流（当前项目）+ 浏览器画面直播（蜘蛛）
- SSE 事件按 project_id 过滤；切目标时 reset 事件清屏后接新目标数据

## 影响范围

- `pkg/web/projects.go`（新增：存储 + CRUD + runManager + 导出）
- `pkg/web/console.go`（路由扩展；Deps 移除，web 直接用 engine——本就允许该方向 import）
- `pkg/web/console.html`（重构）
- `cmd/argo.go`（runWebConsole 简化）

## 风险

1. **结果体量**：PendingUrl 含 base64 req/resp 时项目文件可能很大——默认 web 任务强制 NoReqRspStr
   （`--norrs` 语义），导出仍含 method/url/status 等核心字段。
2. **并发写盘**：单 runManager 协程顺序写，无竞争。
3. **v1 API 兼容**：/api/task 移除，前端同版本发布无兼容负担。

## 子任务

- [x] projects.go 存储与 CRUD（webdata/ 目录原子写；行为全经端到端验证）
- [x] runManager（顺序目标、stop、失败标 failed 续行；修复 markTarget 写锁内取读锁的死锁——实测 API 全挂起）
- [x] 导出 txt/json/csv（Content-Disposition 下载，文件名取项目名）
- [x] API 路由 + SSE 事件带 project_id（前端过滤非当前项目）
- [x] console.html 项目化重构（侧栏项目列表/新建弹层/目标进度点/导出按钮）
- [x] 端到端：建项目(2目标)→顺序执行→死端口目标正确标 failed(count=0)→重启进程项目与结果仍在→导出三格式内容正确

## 修复记录（2026-10-03 梳理）

- [x] runLoop MaxDepth 缺 0 守卫：API 直建项目不带 params 时把全局 maxdepth 压成 0、
      depth 过滤只剩首页整目标白跑；补 `>0` 守卫（与 TabCount 一致）
- [x] quiet 组合修复：config.yml 开 quiet 时 resultHandlerWork 只打印不进 ResultList，
      web 控制台实时流/项目结果全空；runLoop 强制关 quiet、目标跑完还原
- [~] 已知不修：① SSE 目标切换边界（≤500ms 窗口）可能把上一目标尾部条目标成新目标 id（纯展示层）；
      ② 项目内被 stop 的目标重开项目后永远跳过，无法续跑（当前设计，需重开项目）

## 追加：结果统计条 + 展示不全修复（2026-10-03）

- [x] 左栏统计条 GET/POST/未请求 计数（口径与列表计数一致；批量加载按全量结果算）
- [x] 修复① 项目详情 slice(-500) 截断但计数全量 → 全量渲染，
      >3000 条仅渲染尾部并提示（防 DOM 卡死），统计/计数仍按全量
- [x] 修复② 运行中切走再切回当前目标前半段丢失（SSE 游标不回退 + store 目标
      结束才落盘）→ selectProject 对 live 项目重建 SSE 连接从头推
- [x] 实测 rod 驱动真实页面：438 条项目渲染 438 行=计数，统计与结果文件一致

## 追加：POST 高亮（2026-10-03）

- [x] Web：POST 徽标橙色加粗（#ff9e64）+ 行左侧橙色边条，GET 不变
- [x] 终端：结果日志 POST 行方法段亮紫(95)高亮（resultHandlerWork），
      实测爬取 9 条 POST 全部命中；rod 实测 Web 颜色断言通过
