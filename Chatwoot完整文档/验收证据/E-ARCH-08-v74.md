# E-ARCH-08-v74 PushService IAM 用户时区读取边界

`PushService` 在判断静默时段时原先从 NTF 数据库直接查询 IAM `User`。本轮显式注入 IAM `UserRepository`，用户时区只通过 IAM 查询；NTF 继续保存通知设置、设备订阅和投递日志，不再要求存在 User 权威表。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `4beaa09`；E-ARCH-08-v73 |
| 阶段分库检查 | 77 个子用例通过 |
| 专项回归 | IAM 独立库返回用户时区，NTF 使用该时区命中静默时段并阻止网关请求；NTF 无 User 表，IAM 无 NotificationSetting 表 |
| 依赖清单 | 新增 NTF→IAM UserRepository 源码边；依赖摘要 `4d82ba1e6147d315f4c57ebcdc8005d9f2d9c19d979a805b27d95355d3a71f72` |
| 协作合同 | 新增 PushService 对 UserRepository.FindByID 的 IAM 查询绑定；协作摘要 `2da0d22e0d1938460f43312a0cc6cc70a79b972fc647c90ee65b7f1423a64c55`；模块合同摘要保持 `76772e4a573bdafc1ac609469ac2fd774eb6505e24d3fa0fdafff99b0eb7699a` |
| 完整回归 | `make test` 通过；77 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；PushService 已增加禁止通过 `s.db` 查询 User 的门禁 |

PushService 的 IAM 用户时区边界已经完成。后续扫描确认下一项为 `EmailService` 邮件模板渲染中的 ENT `BrandedEmailLayout` 读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
