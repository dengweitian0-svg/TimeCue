# internal/storage/sqlite/migrations

- 本目录保存 SQLite 的版本化 SQL 迁移文件，由上级 `migrate.go` 使用 `go:embed` 注册并按版本顺序执行。
- 当前文件：
  - `001_initial.sql`：创建迁移记录表、日程表、日程起始时间索引，以及后续实例覆盖和提醒尝试所需的关联表。
  - `002_recurring_cutoff.sql`：追加可空的重复截止日期文本列 `until_date_local`，保留旧行和关联表。
  - `003_month_end_policy.sql`：追加可空的月末策略列 `month_end_policy`，约束为 `NULL` 或 `clamp`，保留旧行和关联表。
  - `004_occurrence_snapshots.sql`：追加日程备注，并将例外表扩展为完整快照，保留原有 `occurrence_local` 身份列和无快照取消预留。
  - `005_schedule_open_invariant.sql`：修复遗留的单次关闭状态，并用插入/更新触发器阻止未来单次日程关闭。
  - `006_reminder_attempts.sql`：为提醒尝试建立稳定身份唯一索引，并限制合法状态和终态转换。
  - `007_reminder_processing.sql`：追加可空的提醒首次确认时间和待处理查询局部索引，不回填历史记录。
- 迁移文件必须编号递增；迁移流程依靠同事务提交的版本记录保证重复启动幂等，ALTER TABLE 不要求脱离版本管理单独重复执行。已经发布并可能被使用的迁移不得直接修改，结构变更应追加新的迁移文件。
- 表约束是持久化事实的最后守卫：布尔状态使用 `0/1`，正数状态保持大于零，关联记录使用外键和唯一约束；新增约束时必须评估已有数据和回滚行为。
- 时间字段的存储格式必须与 `sqlite` 存储实现的解析规则一致：本地墙钟时间保留 RFC3339 文本，审计时间使用 UTC 文本。
- 新增迁移后必须同步更新 `migrate.go` 的版本注册和迁移测试，并验证中途失败不会留下部分结构或错误版本记录。
- 例外快照使用 `(schedule_id, occurrence_local)` 唯一；`open=1` 必须保存实际开始、正时长、标题和备注的完整组，`open=0` 可保留全空或完整快照供取消/恢复使用，禁止部分快照。
- 提醒尝试使用 `(schedule_id, occurrence_local)` 唯一；状态只允许 `claimed`、`submitted`、`failed`、`skipped`，且已占用记录只能推进一次到三个终态。
- `007_reminder_processing.sql` 只追加可空 `reminder_attempts.processed_at_utc` 和 `submitted + NULL` 的局部查询索引；不回填旧记录、不保存正文快照，也不把处理状态绑定到发送状态触发器。
- `008_scheduler_recovery.sql` 只新增 `id=1` 的 `scheduler_recovery_checkpoint` 单例表，保存恢复 checkpoint 秒数和 UTC 审计时间；不得新增 occurrence 实例表或把恢复子状态持久化到数据库。
