# HTTP 请求处理

将 HTTP 输入转换为业务调用，并构造接口响应。

- `create_schedule_handler.go`、`get_schedule_handler.go`、`delete_schedule_handler.go`：日程创建、详情和软删除。
- `list_schedule_handler.go`：日程列表、日期安排、月份标记及日期与待确认组合快照。
- `notifications_handler.go`、`notification_handler.go`：历史待确认分页与提醒详情。
- `acknowledgements_handler.go`：单条及批量提醒确认。
- `health_handler.go`：数据库健康检查响应。
- `error.go`：业务错误码、错误映射和公共参数解析。

工程约束：

- 每个接口处理函数独立成文件，请求/响应 DTO 放在对应文件；handler 只解析输入、调用 service 并返回 `middleware.HTTPResult`，不直接访问 SQLite、写响应或写日志。
- 内部错误通过 `Cause` 传递，公开响应使用稳定错误码；预期的 4xx 拒绝不设置 `Cause`。供路由提取业务日志字段的 DTO 需可跨包访问。
- JSON 写入接口拒绝未知字段、非 JSON 和超过 1 MiB 的请求体。提醒确认统一提交 `notificationIds`，单条使用单元素数组；业务时间输出固定 `+08:00`。
