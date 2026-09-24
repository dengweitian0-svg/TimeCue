# SQLite 迁移

迁移使用独立 SQL 文件。Go 的 `Sqlite.Connect` 只打开数据库，不会建表或自动升级结构。

在仓库根目录首次建立数据库时执行：

```powershell
New-Item -ItemType Directory -Force db | Out-Null
sqlite3 ./db/timecue.db ".read migrations/001_initial.up.sql"
```

`001_initial.down.sql` 是对应的回滚脚本，会删除提醒和日程表及其中的数据。后续结构调整应新增编号迁移文件，不修改已经部署过的迁移。
