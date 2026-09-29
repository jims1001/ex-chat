# E-ARCH-08-v159 BIL 订单、付款与余额查询

| 验收项 | 结果 |
|---|---|
| 订单查询 | 支持内部 ID、业务订单号、物流单号，始终附加 account_id |
| 付款查询 | AccountBilling 按 account_id + id 读取，外账号流水返回不存在 |
| 余额查询 | 仅汇总本账号 deposit/deduction，返回币种和流水数 |
| 数据最小化 | 订单视图不返回客户姓名、邮箱、电话、地址和商品 JSON |
| 存储隔离 | BIL 独立库不创建 Account、Contact、Conversation、User 外部所有者表 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。BIL 尚有命令、事件、容量、回退和恢复验收，ARCH-08 保持 REWORK。
