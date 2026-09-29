# E-ARCH-08-v172 NTF 通知偏好与设备注册命令

| 验收项 | 结果 |
|---|---|
| 成员边界 | 两类命令均通过 TEN `ten.permissions.check.v1` 验证目标用户 |
| 偏好约束 | 三类通知渠道只接受冻结通知类型并去重，静默时段严格使用 HH:MM |
| 设备约束 | 只接受 fcm、apns、browser，Token 必填并限制长度 |
| 重复注册 | 同账号、用户、类型和 Token 更新原设备事实，不重复插入 |
| 秘密最小化 | PushToken 不进入响应或命令收据，仅留在 NTF 自有数据库 |
| 幂等与隔离 | 重放复用收据，非成员不写事实；NTF 不读取 IAM/TEN 用户表 |

专项测试 `TestNTFPreferenceAndDeviceCommandsValidateMemberAndHideToken` 通过；模块运行时、模块隔离、完整 Go 回归、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均随本批执行通过。NTF 尚有投递、账号清理、事件、容量、版本回退、自动投递和恢复验收，ARCH-08 保持 REWORK。
