# SQLite 存储

- `db.go` 管理连接和健康检查，`migrate.go` 管理初始化与升级。
- `schedule.go` 和 `schedule_change.go` 持久化日程、版本和写入回执。
- `notification*.go` 管理提醒查询、加载、投递、成员和历史恢复事务。
- `chat.go` 管理会话、运行、模型调用、事件和 checkpoint。`chat_tools.go` 管理工具事务，`chat_usage.go` 读取原始用量。
- `*_test.go` 覆盖真实 SQLite 迁移、查询和事务回归。

## 工程约束

- 升级可识别的旧库前做一致性备份，未知结构拒绝自动迁移。
- 写入回执及调用身份在事务提交后发布。聊天存储通过进度模块处理消息配对与状态收尾。
