# E-ARCH-08-v155 KB 五类查询边界

| 验收项 | 结果 |
|---|---|
| 后台内容 | `kb.content.get.v1` 强制 account_id+article_id，不预加载 Category/User |
| 版本摘要 | `kb.version.get.v1` 返回状态、更新时间和内容哈希，不返回正文 |
| 公开视图 | 同账号 Portal 与 published Article 双重约束，草稿和跨账号内容不可见 |
| 门户读取 | 支持账号内 ID 或 slug，响应不携带关联集合 |
| 删除状态 | 分别统计账号内 Portal、Category、Article 并计算 readiness |
| 数据边界 | 独立 KB 库不创建 User、Account、MediaAsset 外部所有者表 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均纳入本批验收。KB 尚有命令、事件、容量、回退与恢复验收，ARCH-08 保持 REWORK。
