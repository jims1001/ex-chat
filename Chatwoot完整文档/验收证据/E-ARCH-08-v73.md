# E-ARCH-08-v73 PublicHandler CUS 联系人回读与 MSG 幂等查询边界

`PublicHandler` 创建联系人并合并自定义属性后原先直接回读 CUS `Contact`，创建消息前也直接按 `conversation_id + echo_id` 查询 MSG `Message`。本轮复用 CUS `UpdatePublic` 返回的账号内联系人结果，并新增 MSG `FindByEchoID` 查询；该查询同时限定账号、会话和 echo ID。处理器的数据库句柄已完全移除。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `aee4701`；E-ARCH-08-v72 |
| 阶段分库检查 | 76 个子用例通过 |
| 专项回归 | CUS 自定义属性更新及回读、跨账号拒绝通过；MSG echo 查询按账号和会话隔离，同 echo ID 不会泄漏其他账号或会话；CUS/MSG 库均无对方权威表 |
| 依赖清单 | 复用既有 API→CUS/MSG 源码边；依赖摘要保持 `1940ba09da68fc23906f884f993de1e7f6b80d110671e1adc6d90279d577237d` |
| 协作合同 | 新增 MessageRepository.FindByEchoID 的 MSG 查询绑定；协作摘要 `3993178fc957885acd1d1f4f3e8967e57bcf122d942249f8838fb83fad36c88f`；模块合同摘要保持 `76772e4a573bdafc1ac609469ac2fd774eb6505e24d3fa0fdafff99b0eb7699a` |
| 完整回归 | `make test` 通过；76 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；PublicHandler 已增加禁止直接使用 `h.db` 的门禁 |

PublicHandler 的 CUS 联系人回读与 MSG echo 幂等查询边界已经完成。后续扫描确认下一项为 `PushService` 静默时段计算中的 IAM 用户时区读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
