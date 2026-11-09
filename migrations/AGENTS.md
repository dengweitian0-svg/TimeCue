# SQLite 迁移

- `001_initial.*.sql` 管理日程与提醒基础表。
- `002_schedule_changes.up.sql` 增加规则版本、投递停止和写入回执。
- `003_ai_chat.up.sql` 增加聊天持久化表。
- `embed.go` 提供嵌入迁移资源，[README.md](README.md) 说明初始化、备份升级和回退。
