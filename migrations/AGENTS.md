# 数据库迁移

- `001_initial.*.sql`：原有核心业务表；保持已部署文件不变。
- `002_schedule_changes.up.sql`：规则 revision、版本生效区间、永久停止投递、历史文本与创建/更新操作回执；升级后 user_version 为 2。
- `README.md`：新库、已有库升级与回滚说明。

迁移由 Go 数据生命周期负责，当前仍手动运行 SQL；Flutter 不访问业务 SQLite。新库依次执行 001、002；旧库只追加 002。版本表从旧规则 updated_at 建立起点，不能恢复升级前未保存的暂停区间或旧文字。
