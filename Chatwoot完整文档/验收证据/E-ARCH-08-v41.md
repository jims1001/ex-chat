# E-ARCH-08-v41 AutomationService TEN/IAM 分配边界

原 `AutomationService` 在 AUT 数据库中直接检查 AccountUser、查询 User 和 Team。本轮改为坐席分配通过 TEN 账号成员合同校验，并由 TEN 组合 IAM 用户；团队分配通过 TEN 所有者查询。AUT 只保留自动化规则和执行记录。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `ab3f859`；E-ARCH-08-v40 |
| 阶段分库检查 | 48 个子用例通过 |
| 专项回归 | AUT/CON/TEN/IAM 分库下，合法账号成员和团队分配成功，AUT 不含 AccountUser/User/Conversation 表 |
| 依赖清单 | 补充 AUT→TEN `AccountRepository` 源码边；依赖摘要保持 `0d326344465397bbc651fbce06879fe19bf683bf15d51188d8aa8c5c311002b0` |
| 协作合同 | 复用 TEN/IAM 进程内所有者端口；摘要保持 `93b0298edced86759832801a5086083e885fd024d03ba571d28a624c291b89a0` |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复 AUT 对 AccountUser/User/Team 的直接查询 |

同时修正 E-ARCH-08-v40 中 MSG 投递聚合对带切片结构的 GORM 解析警告。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `AutomationService` 自动化条件中的 CUS 联系人/公司访问边界。
