# HTTP 请求处理

将 HTTP 输入转换为业务调用，并构造接口响应。

- `create_schedule_handler.go`、`get_schedule_handler.go`、`delete_schedule_handler.go`：日程创建、详情和软删除；`update_schedule_handler.go` 接入同类型事务更新。
- `list_schedule_handler.go`：日程列表、日期安排、月份标记及日期与待确认组合快照。
- `notifications_handler.go`、`notification_handler.go`：历史待确认分页、任务/attempt 成员分页与提醒详情。
- `acknowledgements_handler.go`：单条及批量提醒确认。
- `claim_delivery_handler.go`、`start_delivery_handler.go`、`delivery_results_handler.go`、`delivery_executor_exit_handler.go`：完整任务领取、展示开始、结果回填和校验当前运行身份的执行者退出通知；`delivery_http_helpers.go`：严格 JSON 解析、业务错误映射及受控日志字段。
- `health_handler.go`：数据库健康复查和成功/失败响应，可选携带与 ready 相同的不可变运行身份、PID、HTTP 版本及已实现能力。
- `create_run_handler.go`、`get_run_handler.go`、`run_events_handler.go`、`cancel_run_handler.go`、`get_session_handler.go`、`list_sessions_handler.go`、`delete_session_handler.go`：聊天接口请求校验、应用服务调用和直接响应 DTO；`chat_http_helpers.go`：聊天 ID、时间、输入及业务错误适配。
- `error.go`：业务错误码、错误映射和公共参数解析。

# 工程约束
- 每个接口处理函数独立成文件，请求/响应 DTO 放在对应文件；handler 只解析输入、调用 service 并返回 `middleware.HTTPResult`，不直接访问 SQLite、写响应或写日志。
- 内部错误通过 `Cause` 传递，公开响应使用稳定错误码；预期的 4xx 拒绝不设置 `Cause`。供路由提取业务日志字段的 DTO 需可跨包访问。
- JSON 写入接口拒绝未知字段、非 JSON 和超过 1 MiB 的请求体。提醒确认统一提交 `notificationIds`，单条使用单元素数组；业务时间输出固定 `+08:00`。
- claim 的 limit 缺省 20，允许 1–20，显式 null 拒绝；start 仅接受空对象，结果只接受 outcome=sent/failed。成员及文案由 Go 冻结，客户端不能提交成员集合。重复 start 的 shouldDisplay 不构成再次调用平台的许可。
- executor-exit 只接受当前 health instanceId 和明确 attemptIds 数组；空数组是 no-op，未知/重复 ID 安全忽略，格式无效或数组元素为空返回 400。
- 任务查询拒绝未知/重复参数及空身份；deliveryTaskId 与 state 互斥，attemptId 必须属于该任务。原待确认模式保持忽略额外参数及空 cursor 的兼容行为。旧任务/尝试 404，未开始尝试及成员版本游标变化 409；查询不推进恢复或自动确认。
- 创建的 period.open 可选，默认 true；PUT 必须带正整数 expectedRevision，返回递增 revision 和 effectiveAt。创建/更新接受可选 Idempotency-Key，同键同内容返回原结果，异内容及过期草稿返回 409；已到期单次只能保留原触发时间。
- `schedule_log.go` 将 service 的受控摘要转换为 `HTTPResult.LogFields`；创建/更新/删除附加日程 ID、类型和 mutation，幂等重放附加成功消息 `schedule write replayed`。仅生成素材，不直接写日志；内部素材不进入响应 JSON，业务拒绝与回滚不附加成功摘要。
- scope 仅用于 state=unacknowledged，接受 today/history；拒绝空值、重复、非法值及与 deliveryTaskId/attemptId 混用。scoped 模式拒绝未知参数，无 scope 模式保留原有额外参数及空 cursor 兼容行为。
- 聊天 Handler 使用 `internal/agent/chat.Service`；session/run 路径 ID 必须是规范 UUID v4，创建输入严格 JSON 限制为 1 MiB，并且仅接受 deepseek-flash。SSE 保留下层事件 ID 和顺序，断连只关闭订阅，不取消 run。
