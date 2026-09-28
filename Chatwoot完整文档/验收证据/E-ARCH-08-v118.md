# E-ARCH-08-v118 MED 上传创建与资产查询

MED 从空运行壳开始接入真实所有者能力。本轮新增 MediaAsset、UploadSession、SecurityScan 和 AccessGrant 四类权威数据，更新数据库所有权和 ARCH-02 对象索引。

| 验收项 | 结果 |
|---|---|
| 上传创建 | `med.upload.create.v1` 仅接受 1–30MB 的有效文件元数据，原子创建资产与上传会话 |
| 令牌保护 | 响应返回一次性原始令牌，MED 库仅保存 SHA-256 TokenHash |
| 幂等重放 | 运行时重建后重放同一 idempotency_key，返回同一资产、会话和令牌，仅一条命令收据 |
| 资产查询 | `med.asset.get.v1` 强制 account_id + asset_id，跨账号返回未找到 |
| 响应最小化 | 只返回文件类型、大小、校验和安全状态，不返回 StorageKey、TokenHash 或授权详情 |
| 库边界 | MED 独立库无 Account、Attachment 和 Message 表 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MED 上传确认和安全扫描状态机；ARCH-08 保持 REWORK。
