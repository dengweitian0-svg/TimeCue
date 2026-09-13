# internal/schedule/domain

- 本目录是日程领域核心，只使用标准库和自身类型，负责表达日程状态、时间规则及机器可读业务错误。
- 当前文件：
  - `model.go`：持久化日程 `Schedule`、创建候选 `ScheduleCandidate`、更新候选 `ScheduleUpdateCandidate` 及其兼容别名。
  - `time.go`：时间快照、时间边界、单次日程端点校验、闰日夹取和本地时间格式化。
  - `errors.go`：校验、时间越界、资源不存在和版本冲突错误及错误链保留。
  - `recurrence.go`：严格每日/每周 cron、重复候选校验和无副作用发生计算。
  - `*_test.go`：验证领域模型、边界、闰日和错误行为。
- `CalculateTimeBounds` 必须基于当前时间对应的本地日期计算边界；单次日程从本地当天开始，结束边界为一年后，查询边界再覆盖结束边界所在的完整本地日期。
- `ValidateSingleEndpoints` 必须在生成候选前验证时区、分钟精度、端点先后和允许范围；生成的单次候选必须保持 `Cron == nil`、`Open == true`，审计时间使用 UTC。
- `ScheduleUpdateCandidate` 只表达允许更新的字段、显式 `Cron` 存在性、期望版本和 UTC 审计时间；可更新字段的指针为 nil 表示保持原值，而显式字段存在性不能被值的零值替代。
- `StaleRevision` 表示乐观并发控制中的版本冲突，必须保留机器可读分类和字段信息，不能降级为普通校验或存储错误。
- `AddCalendarYearClamped` 必须保留 2 月 29 日的同月同日语义，将非闰年的结果夹取到 2 月 28 日；不得直接依赖 `time.AddDate` 的默认归一化结果。
- 领域错误必须保留分类、字段和可安全暴露的边界信息；底层原因通过 `Unwrap` 保留，但不得在对外 JSON 中泄露。
- 本目录不得读取系统时钟、访问数据库、记录日志或依赖 `app`、`clock`、`storage`；所有环境输入必须显式传入，保证规则可测试。
- `ParseCron` 只接受 `M H * * *` / `M H * * D`，分钟 0–59、小时 0–23、星期 0–6（0 为周日）；不接受别名、范围、列表、步长或秒字段。
- `ValidateRecurring(snapshot, startUnix, endUnix, cron, untilDateLocal)` 使用首次实际起止差计算持续分钟数；首次时间是包含式下界，不要求与 cron 匹配。重复定义允许历史首次时间，不应用单次日程的滚动日期范围；截止日期必填、不得早于首次本地日期，保存为 `UntilDateLocal` 的 `YYYY-MM-DD` 文本。应用边界负责将 until_unix 转为此日期，领域不把日期还原为可能因 DST 缺失的午夜瞬间。
- `NextBase(schedule, after, location)` 返回 `(Occurrence, bool, error)`；`NextStart` 只投影其开始时间。`after` 严格排除，首次和截止日期包含；`Expand(schedule, from, to, location)` 按开始时刻展开 `[from, to)`，不受当前时间或 Open 状态影响。
- 发生计算把首次墙钟日期/时间放到显式时区解释，使用自然日/星期推进；DST 缺失按偏移跳变幅度后移，重复时刻取较早时刻。结束时间按实际持续分钟数相加，`Occurrence` 不持久化。
