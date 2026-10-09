# SQLite 迁移

迁移使用独立 SQL 文件。Go 的 Sqlite.Connect 只打开数据库，不会建表或自动升级结构。当前代码要求迁移版本 2，目录提供 001/002。

[D1 实现](../docs/phase-d1-minute-scheduler.md)直接修改原 001 中 notifications.status 的 CHECK，允许 pending/loaded/sent/failed。仍使用四张业务表、版本 2，不新增表、字段、迁移或中间表。

首次建立数据库时，在仓库根目录执行：

```powershell
New-Item -ItemType Directory -Force db | Out-Null
sqlite3 -bail ./db/timecue.db ".read migrations/001_initial.up.sql"
sqlite3 -bail ./db/timecue.db ".read migrations/002_schedule_changes.up.sql"
```

上述命令用于新库，测试直接读取当前 SQL 建立临时数据库，迁移目录不提供 Go 包。修改文件不会自动改变已有库：旧 status CHECK 不支持 loaded 时，即使 user_version 为 2 也会被健康检查明确拒绝。不要对已有数据直接重跑建表或删除数据库；旧库的结构调整不由启动流程自动执行。

002 保存规则版本及有效区间、提醒历史文本、永久停止标记及日程写入幂等回执。旧记录以升级时可读内容回退，升级前未记录的启停区间和旧名称无法还原。

001_initial.down.sql 会删除原业务表及数据。002 只提供升级脚本；回退应恢复原数据库备份。
