# E-ARCH-08-v61 CRMOpsAdvancedHandler 公司联系人关联所有者边界

原 `CRMOpsAdvancedHandler` 在批量关联联系人后直接从 ORC 数据库预加载 CUS `Contact.Company/Labels`，解除关联前也直接查询 CUS `Contact`。本轮新增 CUS `CompanyRepository.ListContactsByIDs` 和 `ContactBelongsToCompany`：关联写入后的响应由 CUS 按账号回读，解除前由 CUS 同时校验账号、公司和联系人归属，handler 不再直接访问联系人权威数据。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `f5197c3`；E-ARCH-08-v60 |
| 阶段分库检查 | 64 个子用例通过 |
| 专项回归 | CUS 独立库下关联后可回读当前账号联系人及 Company/Labels，解除前归属检查和解除操作通过；跨账号联系人不进入回读结果且不能通过归属检查；CUS 库不含 CON 权威表 |
| 依赖清单 | 复用已登记的 ORC→CUS `CompanyRepository` 源码边；依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `CompanyRepository.ListContactsByIDs`、`ContactBelongsToCompany` 读取合同；协作摘要 `2d90aa498cc14e665f833e13acaa15fc5babc513c1e3ac77055d457f49e078a0` |
| 完整回归 | `make test` 通过，公司与联系人关联及公司管理增强回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器、网络及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止恢复 handler 的联系人回读和解除前直接查询 |

CRMOpsAdvancedHandler 公司联系人添加回读及解除关联的 CUS 访问边界已经完成。下一项为 `CRMOpsAdvancedHandler.GetConversationSLA` 的 CON/CHN 会话及收件箱预加载边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
