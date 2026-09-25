# E-ARCH-08-v10 TEN 团队与账号成员分库

## 当前结论

ARCH-08 仍为 REWORK。TEN 团队和账号成员查询通过 IAM 用户查询端口补齐资料；不再联表或自动预加载 IAM 用户表。团队和账号成员保存排除外部所有者关联对象，避免更新成员关系时写回用户资料。团队删除仅清理所属账号的关系，并传播清理错误。

IAM 中无人调用的反向联表账号查询已移除，账号成员归属继续由 TEN 维护。当前仍为进程内端口，不代表独立进程运行完成。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v10 |
| baseline_reference | E-ARCH-08-v1 至 v9；Git `a3a4caf` |
| environment | 本地 macOS；TEN/IAM 分离 SQLite 库；容器测试环境 |
| result | 阶段分库 gate 18 个子用例通过；ARCH-08 整体未通过 |

## 验证

- 新增团队和账号成员分库用例：查询正确、不写回 IAM、不创建外部用户表、跨账号团队删除不影响关系、缺失用户查询端口返回错误。
- 阶段分库竞争检查通过：`EX_CHAT_MODULE_ACCEPTANCE=1 go test -race ./test -run '^TestModuleIsolationAcceptance$' -count=1`。
- `make test`、`make test-docker`、`go vet ./cmd/... ./internal/... ./pkg/... ./test/...` 通过。
- 移除未使用方法后再次执行全包编译检查；`git diff --check` 通过。

完整剩余工作与 26 模块验收矩阵见 [剩余验收清单](ARCH-08-剩余验收清单.md)。源码引用候选数包含合法类型引用，并不等于违规数；本次账号成员 Preload 访问说明旧脚本还会漏掉隐式访问。ARCH-02/03/04 保持 REOPENED，ARCH-08 保持 REWORK。
