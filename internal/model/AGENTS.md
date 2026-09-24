# internal/model

- `Schedule` 只允许单次规则与周期规则二选一，创建前通过 `Validate` 检查结构；cron 语法由规则计算部分验证。
- `Schedule.Deleted` 与 `Notification.Deleted` 是软删除标记，删除后记录仍可用于历史查询。
- `Schedule.Cancelled` 表示终止日程；`Period.Open` 表示周期日程可恢复的暂停。
- `Notification.Status` 保存投递结果，`AcknowledgedAt` 单独保存用户确认时间。
- `QueryOption` 的字段均为可选条件，未设置的指针字段不参与查询；`GetScheduleByID` 的必填日程 ID 作为独立参数，`GetAllSchedule` 不要求任何必填条件。
- 新增可导出函数与关键规则应提供中文注释。
