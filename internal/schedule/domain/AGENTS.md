# internal/schedule/domain

- 本目录是日程领域核心，只使用标准库和自身类型，负责表达日程状态、时间规则及机器可读业务错误。
- 当前文件：
  - `model.go`：持久化日程 `Schedule`、创建候选 `ScheduleCandidate`、更新候选 `ScheduleUpdateCandidate`、发生和例外快照模型，以及取消/恢复操作候选和结果。
  - `time.go`：时间快照、时间边界、单次日程端点校验、闰日夹取和本地时间格式化。
  - `errors.go`：校验、时间越界、资源不存在和版本冲突错误及错误链保留。
  - `recurrence.go`：严格每日/每周/月 cron、重复候选校验和无副作用基础发生计算；月规则暂由应用边界守卫。
  - `occurrence.go`：某次安排的完整快照校验、基础发生与例外合并以及按实际时间选择下一次发生。
  - `conflict.go`：冲突检查状态、目标与冲突模型，无副作用的下一次批次选择和半开区间冲突计算。
  - `*_test.go`：验证领域模型、边界、闰日和错误行为。
- `CalculateTimeBounds` 必须基于当前时间对应的本地日期计算边界；单次日程从本地当天开始，结束边界为一年后，查询边界再覆盖结束边界所在的完整本地日期。
- `ValidateSingleEndpoints` 必须在生成候选前验证时区、分钟精度、端点先后和允许范围；生成的单次候选必须保持 `Cron == nil`、`Open == true`，审计时间使用 UTC。
- `ScheduleUpdateCandidate` 只表达允许更新的字段、显式 `Cron` 存在性、期望版本和 UTC 审计时间；可更新字段的指针为 nil 表示保持原值，而显式字段存在性不能被值的零值替代。
- `MonthEndPolicy` 是可空的持久化策略字段；更新候选必须用 `MonthEndPolicyPresent` 区分保留策略和显式写入 `NULL`。
- 重复创建候选和日程定义包含 `UntilDateLocal`；更新候选的该指针非 nil 才修改截止日期，本 Issue 不支持清空。应用层复用 `ValidateRecurring` 校验变更的首次端点，并通过 `NextBase` 校验合并后的定义，原始发生身份不进入更新候选。
- `StaleRevision` 表示乐观并发控制中的版本冲突，必须保留机器可读分类和字段信息，不能降级为普通校验或存储错误。
- `AddCalendarYearClamped` 必须保留 2 月 29 日的同月同日语义，将非闰年的结果夹取到 2 月 28 日；不得直接依赖 `time.AddDate` 的默认归一化结果。
- 领域错误必须保留分类、字段和可安全暴露的边界信息；底层原因通过 `Unwrap` 保留，但不得在对外 JSON 中泄露。
- 本目录不得读取系统时钟、访问数据库、记录日志或依赖 `app`、`clock`、`storage`；所有环境输入必须显式传入，保证规则可测试。
- `ParseCron` 只接受 `M H * * *` / `M H * * D` / `M H D * *`，分钟 0–59、小时 0–23、月内日期 1–31、星期 0–6（0 为周日）；日和星期不能同时指定，不接受别名、范围、列表、步长或秒字段。
- `ValidateRecurring(snapshot, startUnix, endUnix, cron, untilDateLocal)` 使用首次实际起止差计算持续分钟数；首次时间是包含式下界，不要求与 cron 匹配。重复定义允许历史首次时间，不应用单次日程的滚动日期范围；截止日期必填、不得早于首次本地日期，保存为 `UntilDateLocal` 的 `YYYY-MM-DD` 文本。应用边界负责将 until_unix 转为此日期，领域不把日期还原为可能因 DST 缺失的午夜瞬间。
- 月规则候选自动携带 `MonthEndPolicyClamp`；已保存月规则必须携带该策略，日/周规则必须保持策略为 nil。月规则按月份首日推进，每月重新将 cron 日期夹取到当月天数，原始日期只从 cron 解析。
- `NextBase(schedule, after, location)` 返回 `(Occurrence, bool, error)`；`NextStart` 只投影其开始时间。`after` 严格排除，首次和截止日期包含；规则校验通过后，`Open=false` 的重复日程不产生发生。`Expand(schedule, from, to, location)` 按开始时刻展开 `[from, to)`，关闭组返回空结果。
- 发生身份使用规则计划日的 `OriginalDate`；例外按 `(schedule_id, original_date)` 替换基础发生，并以实际开始/结束参与查询和排序。例外可独立于父规则截止日期继续发生；关闭组在扫描例外前短路。`OccurrenceOnDate` 保留不依赖 `Open` 的原始日期身份匹配能力，供恢复本次例外使用。
- 取消/恢复操作候选必须携带原始日期、期望父 revision 和 UTC 审计时间；恢复候选只有在无快照取消例外且当前规则重新产生该身份时才能请求移除例外。
- 发生计算把首次墙钟日期/时间放到显式时区解释，使用自然日/星期推进；DST 缺失按偏移跳变幅度后移，重复时刻取较早时刻。结束时间按实际持续分钟数相加，`Occurrence` 不持久化。

- MergedOccurrenceIterator 以半开区间按需产生基础发生和例外合并结果；发生是否命中查询由实际起止区间判断，排序身份必须保留 schedule_id 与 original_date。
- `SelectNextTargetBatch` 返回所有开放日程中严格晚于 after 的最早一批发生，同刻的全部身份同属一批；`DetectOccurrenceConflicts` 使用 `a.start < b.end && b.start < a.end` 比较目标批次和窗口内全部有效发生，检索下界按最长持续时间回看以覆盖跨天实例，只排除目标身份本身而不排除同组其他发生。冲突结果的排序键和同组标识必须稳定，领域不读取时钟、不写库。
