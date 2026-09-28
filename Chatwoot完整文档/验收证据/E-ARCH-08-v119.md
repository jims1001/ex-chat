# E-ARCH-08-v119 MED 上传确认与安全扫描

| 验收项 | 结果 |
|---|---|
| 上传确认 | `med.asset.confirm.v1` 同时校验账号、资产、会话、未过期状态、SHA-256 令牌和文件校验和 |
| 令牌比较 | 使用恒定时间比较，原始令牌不写入日志或资产响应 |
| 状态机 | pending_upload → pending_scan → ready/clean 或 rejected/rejected，终态不可重复扫描 |
| 扫描记录 | clean 和 rejected 均写入 SecurityScan，威胁细节只保存在 MED 所有者库 |
| 事件原子性 | 资产终态、扫描记录、命令收据与 ready/rejected outbox 同事务提交 |
| 事件最小化 | 事件只含 asset_id、公开状态、content_type 和 file_size，不泄露 StorageKey、TokenHash 或 Threat |
| 幂等与租户 | 确认和扫描重放各只有一条收据；跨账号资产返回未找到 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MED 访问授权查询和安全状态查询；ARCH-08 保持 REWORK。
