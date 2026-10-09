# SQLite 迁移

迁移使用独立 SQL 文件。Go 的 `Sqlite.Connect` 只打开数据库，不会建表或自动升级结构。

在仓库根目录首次建立数据库时执行：

```powershell
New-Item -ItemType Directory -Force db | Out-Null
sqlite3 -bail ./db/timecue.db ".read migrations/001_initial.up.sql"
sqlite3 -bail ./db/timecue.db ".read migrations/002_schedule_changes.up.sql"
```

`001_initial.down.sql` 是对应的回滚脚本，会删除提醒和日程表及其中的数据。后续结构调整应新增编号迁移文件，不修改已经部署过的迁移。

阶段 C 要求迁移版本 2。已有版本 1 数据库关闭 Go 服务后只执行：

```powershell
sqlite3 -bail ./db/timecue.db ".read migrations/002_schedule_changes.up.sql"
```

002 保存规则版本和有效区间、提醒历史文本及投递停止状态、创建/更新幂等回执。旧记录以升级时可读内容回退，升级前未记录的启停区间和旧名称无法还原。当前只提供 002 升级脚本；需要回退旧代码时，应恢复升级前保存的数据库备份。
