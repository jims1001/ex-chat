# E-ARCH-08-v49 DataImportService CUS 联系人存在性访问边界

原 `DataImportService` 在 MIG 数据库中直接查询 CUS 所有的 `Contact`，CSV/JSON 预校验与执行阶段共存在四条联系人邮箱查重路径。本轮统一改为调用 CUS `ContactRepository.EmailExists`，联系人创建也由路由显式注入同一个 CUS 所有者仓储；CUS 查询失败会明确返回验证错误或导入行错误，不再被误判为联系人不存在。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `2abd3fe`；E-ARCH-08-v48 |
| 阶段分库检查 | 53 个子用例通过 |
| 专项回归 | MIG/CUS 分库下，CSV 预校验只识别当前账号已有邮箱，跨账号同邮箱不误判；执行阶段跳过当前账号已有联系人并在 CUS 创建新联系人；MIG 不含 `Contact` 表 |
| 依赖清单 | 复用已登记的 MIG→CUS 同步依赖；依赖摘要保持 `628daf9c68e737c9672217156b4be5bf459d23648c7d2412491e4d0222a1ad8c` |
| 协作合同 | 新增 `ContactRepository.EmailExists` → `cus.contact.get.v1` 读取绑定；协作摘要 `9f6c0afa86326a71d8a8a71f2ef9928a7c50df1a2caa2ac2b9fab881dcc400a2` |
| 完整回归 | `make test` 通过，数据导入生命周期及现有接口回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则覆盖 DataImportService 对 Contact 的直接查询 |

DataImportService 的 CUS 联系人存在性访问边界已经完成。下一项为 `CampaignService` 的 CON/CUS 会话标签查询边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
