# E-ARCH-08-v85 独立模块公开合同传输边界

本轮在独立模块运行基础单元上增加统一的 Command/Query HTTP 传输边界。运行时只允许注册当前模块前缀和 v1 版本的能力，调用必须同时通过调用方白名单、Bearer 凭据和合同版本检查，并受请求体及执行超时上限约束。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `7c9be91`；E-ARCH-08-v84 |
| 合同入口 | `POST /contracts/commands/{capability}`；`POST /contracts/queries/{capability}` |
| 所有权门禁 | 能力名必须以当前 `MODULE_ID` 的小写前缀开头并以 `.v1` 结尾；外部模块能力无法注册 |
| 调用认证 | `X-Module-Caller` 必须属于 `MODULE_ALLOWED_CALLERS`；Bearer 凭据使用常量时间比较；缺失或错误返回 401 |
| 版本及追踪 | `X-Contract-Version` 必须匹配；`X-Correlation-Id` 回显，Command 信封必须与其一致 |
| 资源上限 | `MODULE_MAX_REQUEST_BYTES` 默认 1 MiB、最大 10 MiB；`MODULE_MAX_TIMEOUT_MS` 默认 10 秒、最大 60 秒；Command 自身超时不能突破运行时上限 |
| 错误语义 | 使用 `foundation.ContractError`，认证、校验、权限、未找到、冲突、依赖失败及结果未知映射为稳定 HTTP 状态和重试属性 |
| 专项测试 | 正常 Query 分发、跨所有者注册拒绝、错误调用方/凭据/版本拒绝、Command 相关 ID 校验、运行时超时截断、稳定错误映射全部通过 |
| 阶段分库检查 | 84 个子用例全部通过 |
| 完整及容器回归 | `make test`、`make test-docker` 通过，Docker 临时资源已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 运行清单摘要 | `67fc8e5da1e1e9e7b0f97d3f5a31cad204c1f28e18f784378da9b39b7c1f2168` |

26 个模块均已登记 `contractTransportImplemented=true`，但仍保留 `independentRuntimeImplemented=false`：当前传输层没有把各模块真实业务能力处理器接入独立进程。下一项从 IAM/TEN 开始接入真实 Query/Command 能力，并验证调用方停止、恢复和超时。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
