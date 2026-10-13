# RPT 部分结果与年度均值的依赖失败传播（phase56）

## 最小修复范围

`GetConversationsReport` 的 agent 分支原先在 CON owner 的实时指标读取
或逐客服会话读取失败时直接跳过该客服。因此，失败请求可能得到空列表
或缺少部分客服的 HTTP 200。本批任一读取失败立即返回原始错误及 nil，
丢弃此前已经计算的结果；附带部分行的错误响应也不能成为成功结果。

`GetYearInReview` 原先忽略 RPT `AVG(first_response)` 聚合查询错误，把
查询失败伪装为零秒平均首响。本批检查同一条查询的错误并立即返回 nil
及原始错误。健康无事件的 `COALESCE(AVG(value), 0)` 仍为零。

对应既有产品合同：`20-报表指标口径筛选与返回字典.md` 的专项分析
`reports/conversations`，以及 `08-报表SLA帮助中心与搜索API.md` 的
Year in Review 字段。此修复不引入新的统计定义。

## 现有 HTTP 合同

沿用处理器已有的 HTTP 500 及固定 JSON 错误包络：

- V1/V2 `reports/conversations`：`Failed to get conversations report`
- V1/V2 `reports/year_in_review` 及 V2 `year_in_review`：
  `Failed to get year in review`

失败响应不包含成功数组、年度数据或内部错误哨兵；健康响应保持直接
数组／对象，不加 data 包装。服务层保留依赖错误身份。既有处理器日志
行为不在本批范围，不宣称全局日志脱敏或新的统一错误分类。

## 成功路径保持不变

- 客服顺序仍取自 TEN 成员列表；先分页再跳过 nil User。健康空页面和
  仅含 nil User 的页面仍成功返回空数组，无 CON 查询。
- 每个有效成员仍先读取 account live metrics，再读取该客服的会话。
  不优化 N+1、不重排查询、不改变 open/unattended/unassigned/pending
  的既有计算，也不改变 account 分支与原有字段。
- 年度报表继续按 account、user、first_response 筛选，使用 UTC
  `[year-01-01, (year+1)-01-01)`，保留零事件值、均值转 int 的截断及
  最繁忙日期的原有排序。CON 读取失败仍先于 RPT 查询返回。
- 不改变 broader FRT、resolution、营业时间政策、共享成员审计、SLA、
  路由权限或模块所有权。无外部 provider、写入、推送或部署范围。

## 准备回归与状态

服务 stub 覆盖 CON 第 1／2／3／4 次读取失败、部分行+错误、立即停止、
下一请求恢复、分页顺序、nil 用户、空成员和 account 兼容。独立 owner
SQLite/PostgreSQL 场景覆盖完整数组恢复、租户与表隔离，以及 RPT Row
回调失败和真实 SQL 表不可用后的恢复。年度数据包含跨用户、跨租户、
年度边界、分数均值与无事件样本。

真实 V1/V2 路由准备覆盖上述 CON 失败点、年度三条已注册路径、权限
先行、通用 500、错误哨兵隔离和成功响应形状。原生清单新增 46 个精确结果、2 个根，是待执行验收
义务，登记不能代表测试通过。代码准备与静态检查不等于运行验证。
第五十五批未独立接受前，本批不运行 Go 测试、构建、服务或完整门禁；
最终验收以同一冻结提交的独立 focused 与完整 core/native 证据为准。
