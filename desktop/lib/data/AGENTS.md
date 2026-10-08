# 界面数据接入

- `http_notebook_repository.dart`：读取 Go 的今日、日期、月历和待确认快照，处理分页、提醒详情、确认、时间转换与错误状态。
- `demo_notebook_repository.dart`：提供内存示例日程与提醒、预设规则投影、创建/编辑/删除和确认交互。

工程约束：HTTP 模式使用 Go 的快照、总数、游标和 occurrenceKey，RFC3339 转换为 UTC+8 展示；请求失败不回退示例数据。日程路径为 `/api/v1/scheduler/schedules`，提醒路径为 `/api/v1/notifications`；确认统一使用 `notificationIds` 数组。
