# API 集成测试

- `router_test.go`、`health_runtime_test.go` 和日志相关测试覆盖路由、健康检查及请求日志。
- `chat*_test.go` 和 `model_revision_test.go` 覆盖聊天、准入、SSE 和配置发布。
- `delivery_test.go`、`recovery_test.go`、`schedule_change_test.go` 和 `unacknowledged_scope_test.go` 覆盖日程与提醒契约。

## 工程约束

- 使用隔离 SQLite 数据。捕获全局日志的用例串行运行。
