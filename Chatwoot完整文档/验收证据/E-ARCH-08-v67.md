# E-ARCH-08-v67 CRMOpsAdvancedHandler 集成安装读取所有者边界

集成目录、Shopify 订单入口和 Dialogflow 处理入口原先由 ORC 直接读取 EXT 的 `IntegrationInstallation`。本轮新增账号范围列表及已安装应用查询，由 `IntegrationRepository` 统一访问安装状态和配置；路由层显式注入 EXT 仓储，ORC 不再直接查询集成安装表。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `5ae3fa4`；E-ARCH-08-v66 |
| 阶段分库检查 | 70 个子用例通过 |
| 专项回归 | EXT 独立库下安装列表、Shopify/Dialogflow 已安装配置、禁用后不可见和账号隔离通过；EXT 库无 CON Conversation 或 CUS Contact 权威表 |
| 依赖清单 | 复用 ORC→EXT IntegrationRepository 源码边；依赖摘要 `6b12a468832b7121784bd72ec16ed8e8c7566801f69fd9ce668b4d8c113e7c6a` |
| 协作合同 | 新增 `ListByAccount`、`FindInstalled` 的 `ext.installation.get.v1` 读取合同；协作摘要 `1c2d2558a5bbc38f298419bba6576dcc08be8846f88a0daf3278f3fcc0e3f651` |
| 完整回归 | `make test` 通过；70 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止恢复 ORC 对 IntegrationInstallation 的直接查询 |

集成安装状态与配置的 EXT 读取边界已经完成。下一项为 `CRMOpsAdvancedHandler.ShopifyOrders` 的 CON/CUS 会话联系人读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
