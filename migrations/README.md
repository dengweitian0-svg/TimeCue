# SQLite 迁移

迁移使用独立 SQL 文件。Sqlite.Connect 只打开连接；Go 正式入口在监听前显式调用 PrepareSchema，自动初始化新库或升级可识别的 Flutter 旧库。代码要求迁移版本 2，原 001/002 嵌入 Go 发行文件。

[D1 实现](../docs/phase-d1-minute-scheduler.md)直接修改原 001 中 notifications.status 的 CHECK，允许 pending/loaded/sent/failed。仍使用四张业务表、版本 2，不新增表、字段、迁移或中间表。

首次建立数据库时，在仓库根目录执行：

```powershell
New-Item -ItemType Directory -Force db | Out-Null
sqlite3 -bail ./db/timecue.db ".read migrations/001_initial.up.sql"
sqlite3 -bail ./db/timecue.db ".read migrations/002_schedule_changes.up.sql"
```

上述命令仅用于手动建立新库，正常首次运行不需要 sqlite3。Flutter 版本 1 会应用 002；版本 2 的旧 status CHECK 会事务重建为支持 loaded，保留主键、提醒结果、确认和停止事实以及索引/触发器。升级前通过 SQLite VACUUM INTO 在原库旁保存带时间的 .bak，备份失败不升级；提交前检查结构，失败回滚。Wails 和未知结构明确拒绝并保留，不自动转换。不要对已有数据重跑建表或删除数据库。

002 保存规则版本及有效区间、提醒历史文本、永久停止标记及日程写入幂等回执。旧记录以升级时可读内容回退，升级前未记录的启停区间和旧名称无法还原。

001_initial.down.sql 会删除原业务表及数据。002 只提供升级脚本；回退应恢复原数据库备份。
