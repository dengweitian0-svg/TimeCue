# internal/model

- `Schedule` 只允许单次规则与周期规则二选一，创建前通过 `Validate` 检查结构；cron 语法由规则计算部分验证。
- 推进提醒所需的日程 ID 和基准时间由服务接口直接传入，不保存独立的 `Rule` 类型。
- `BeijingLocation` 定义业务统一使用的固定 UTC+8 时区，不采用夏令时。
- `Schedule.Deleted` 与 `Notification.Deleted` 是软删除标记，删除后记录仍可用于历史查询。
- `Schedule.Cancelled` 表示终止日程；`Period.Open` 表示周期日程可恢复的暂停。
- `ErrRecordNotFound` 是存储查询与服务层之间共享的未找到错误标识；服务层将日程场景转换为 `ErrScheduleNotFound`。
- `Notification.Status` 保存投递结果，`AcknowledgedAt` 单独保存用户确认时间。
- `QueryOption` 是函数式查询选项，字段私有的 `Option` 保存可选条件；存储层通过 `Option` 的只读方法读取归并结果。`GetScheduleByID` 的必填日程 ID 作为独立参数，`GetAllSchedule` 不要求任何必填条件。
- 新增可导出函数与关键规则应提供中文注释。

- 持久化业务模型只有 Schedule 和 Notification，不添加本子或聚合持久化模型；查询直接复用两类记录。
- `notification.go` 中的 `NotificationPosition` 仅保存稳定分页边界；确认守卫错误也归属提醒模型。
- `ErrInvalidAcknowledgement` 区分未来、已删除或无效确认集合，投递状态不参与确认资格。
