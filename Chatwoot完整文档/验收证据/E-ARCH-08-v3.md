# E-ARCH-08-v3 更多分库业务路径复验

## 结论

**ARCH-08 仍为 REWORK。** 在 v2 的消息创建、故障补偿基础上，增加 CON 标记未读通过 MSG 所有者查询，以及 MSG 消息列表通过 IAM/CUS 所有者补全发件人。分离数据库的六个阶段用例均通过，但仓储其余跨所有者访问和模块独立运行没有完成。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v3 |
| baseline_reference | E-ARCH-08-v1、E-ARCH-08-v2 |
| environment | 本地 macOS；分离 SQLite 模块库；Docker PostgreSQL/Redis 组合回归 |
| validation window | 2026-09-25 21:06–21:12 UTC |
| result | 六个阶段分库用例、完整本地回归与静态检查通过；总体 ARCH-08 未通过 |
| scope | 源码与本地测试；未执行生产迁移、独立服务部署或容量/灰度/恢复验收 |

## 验证边界

`make test-module-isolation` 包含 MSG 创建、CON 故障后补偿及重启重放、本地日志失败回滚、CON 标记未读、MSG 发件人补全、单条故障记录不阻断下一批补偿六个用例。标记未读和发件人补全分别使用独立的 CON/MSG 库和 MSG/IAM/CUS 库，确认消费者库未创建其他所有者表。`make test`、`make test-docker`、`go vet`、`git diff --check` 通过；Docker 临时 PostgreSQL/Redis 已清理。组合模式下仍使用同一个数据库；独立服务的网络接口和路由未实现。

CON 删除会话仍直接清理 MSG、RPT、QLT、RTM、TKT、AIC 等模块的数据；多个仓储还有直接跨所有者读写。删除编排涉及多所有者的幂等、失败恢复和物理文件清理，不能只删除 SQL 语句后声称完成。`scripts/audit-module-storage.py` 当前列出 22 个文件、106 种跨所有者模型引用候选，需逐项判断与修复。

因此 ARCH-02/03/04 保持 REOPENED，ARCH-08 保持 REWORK；后续依赖仍不解锁。v1/v2 保留历史事实，本文件只记录本轮新增验证。
