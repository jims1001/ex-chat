# E-ARCH-08-v90 混合版本升级与回退

本轮将运行时发布版本与合同版本分离，并允许新版本显式声明兼容的旧合同版本。IAM/TEN v2 实例可同时接收 v1/v2 协议；回退到只接受 v1 的 TEN 实例后，新实例已写入的成员事实与幂等收据仍可读取和回放。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `8b3bd4a`；E-ARCH-08-v89 |
| 版本配置 | `MODULE_RELEASE_VERSION` 标识部署版本；`MODULE_CONTRACT_VERSION` 标识首选协议；`MODULE_ACCEPTED_CONTRACT_VERSIONS` 显式列出兼容协议 |
| 配置门禁 | 兼容列表必须包含首选合同版本；未声明版本继续返回不支持错误 |
| 混合运行 | release-2 的 IAM/TEN 接受 v1 和 v2 请求，共用前向兼容模块 schema |
| 升级写入 | TEN v2 经 IAM v2 验证后写入成员事实和持久化幂等收据 |
| 回退 | TEN release-1/v1 使用同一数据库接管后，以 v1 请求回放 v2 写入结果，成员事实保持一条且未丢失 |
| 阶段分库检查 | 84 个子用例全部通过 |
| 完整及容器回归 | `make test`、`make test-docker` 通过，Docker 临时资源已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 运行清单摘要 | `e1b37560d633856c1ddf680606deb660a841de27e5b3651c157814c368614aee` |

当前版本切换证据覆盖 IAM/TEN 首组能力，不能表示所有模块升级完成。下一项执行所有者数据库备份恢复、投影重建和就绪恢复验收。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
