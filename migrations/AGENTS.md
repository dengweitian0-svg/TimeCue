# 数据库迁移

- `001_initial.*.sql`：原有核心业务表；notifications.status 允许 pending/loaded/sent/failed，原列、外键、唯一键及索引保持。
- `002_schedule_changes.up.sql`：规则 revision、版本生效区间、永久停止投递、历史文本与创建/更新操作回执；升级后 user_version 为 2。
- `README.md`：新库、已有库升级与回滚说明，以及 D1 方案的迁移规划入口。
- 迁移目录只保留 SQL 与说明文件。Go 测试直接用 os.ReadFile 读取 SQL，不通过 Go 包内嵌迁移。

目录只提供 001/002，数据库版本为 2，不增加中间表。修改建表文件不自动改变旧库，旧 status 约束由健康检查明确拒绝。
