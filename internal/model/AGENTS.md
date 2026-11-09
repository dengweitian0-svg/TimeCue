# 业务模型

- `schedule.go` 和 `schedule_change.go` 定义日程规则、版本及变更事务类型。
- `notification*.go` 定义提醒状态、加载和历史恢复数据。
- `occurrence_view.go`、`recovery_view.go` 和 `unacknowledged.go` 定义只读视图及待确认范围。
- `query_option.go` 和 `query_cursor.go` 定义筛选、分页和游标，`time.go` 提供北京时间工具。
