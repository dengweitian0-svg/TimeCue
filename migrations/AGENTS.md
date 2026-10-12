# 数据库迁移

- `001_initial.*.sql`：原有核心业务表；notifications.status 允许 pending/loaded/sent/failed，原列、外键、唯一键及索引保持。
- `002_schedule_changes.up.sql`：规则 revision、版本生效区间、永久停止投递、历史文本与创建/更新操作回执；升级后 user_version 为 2。
- `README.md`：新库、已有库升级与回滚说明，以及 D1 方案的迁移规划入口。
- `embed.go` 将原 001/002 作为只读资源交给存储启动准备；测试也可直接读取 SQL 建立隔离库。

目录只提供 001/002，数据库版本为 2，不增加持久化中间表。存储 PrepareSchema 负责显式升级旧 status CHECK，升级前备份，失败回滚；健康检查继续拒绝未准备或不兼容结构。
