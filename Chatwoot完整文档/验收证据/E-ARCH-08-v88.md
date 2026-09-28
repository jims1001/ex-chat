# E-ARCH-08-v88 多实例命令竞争与批量 IAM 查询

本轮关闭首个独立运行链路的扩容竞争与 N+1 查询缺口。PostgreSQL 中八个并发运行实例使用同一幂等键竞争时，唯一收据只允许一个实例执行命令，其余实例在唯一键冲突后读取并返回已提交结果。TEN 成员列表将全部用户 ID 合并为一次 IAM 查询。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `62f46f1`；E-ARCH-08-v87 |
| 多实例竞争 | 8 个并发 Command 调用、1 次实际执行、8 次成功结果；使用真实 PostgreSQL 独立 schema 验证 |
| 唯一冲突恢复 | 竞争失败实例退出已中止事务后读取获胜收据，不重复运行业务函数 |
| IAM 批量查询 | `iam.identity.get.v1` 支持 `ids`；TEN 多成员列表固定为单次 IAM HTTP 调用 |
| 完整性 | TEN 对批量结果逐成员核对 ID，缺失身份明确失败，不返回不完整成员视图 |
| 阶段分库检查 | 84 个子用例全部通过 |
| 完整及容器回归 | `make test`、`make test-docker` 通过；并发 PostgreSQL 用例在 Docker 中实际执行，临时 schema 和容器均清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 运行清单摘要 | `81660837679dcb9ce89dab5e837613f56dc916197f5f22dfc6fa35d32cd4f4e2` |

该证据只证明当前 TEN Command 的多实例幂等和 IAM/TEN 查询容量行为，不能外推为 26 个模块全部容量完成。下一项接入模块 Event 持久化投递、重复与乱序消费边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
