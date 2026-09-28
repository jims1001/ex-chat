# E-ARCH-08-v69 CRMOpsAdvancedHandler Dialogflow 预设回复所有者边界

`DialogflowProcess` 在未调用外部 Dialogflow API 时，原先由 ORC 直接查询 OPS `CannedResponse`。本轮新增 `CannedResponseRepository.FindMatch`，按账号执行短码精确匹配或内容模糊匹配；ORC 仅消费 OPS 查询结果，并在所有者查询失败时明确返回错误。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `e4999a3`；E-ARCH-08-v68 |
| 阶段分库检查 | 72 个子用例通过 |
| 专项回归 | OPS 独立库下短码大小写无关匹配、内容匹配、未找到和账号隔离通过；OPS 库无 CON Conversation 或 EXT IntegrationInstallation 权威表 |
| 依赖清单 | 新增 ORC→OPS CannedResponseRepository 源码边；依赖摘要 `d606a4a3c8a8663b8dbbc912c9eeb21e03d83bb58d9bcdbad8fcbac4708cdf32` |
| 协作合同 | 新增 `FindMatch` 的 `ops.canned_response.get.v1` 读取合同；协作摘要 `195f278a52063a3ec726f75f38c12ddadfc9e9476abfe2014620d9ad57f4988d` |
| 完整回归 | `make test` 通过；72 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止恢复 ORC 对 CannedResponse 的直接查询 |

Dialogflow 预设回复的 OPS 读取边界已经完成。下一项为 `CRMOpsAdvancedHandler` 残留数据库句柄清理及整体直接存储复核。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
