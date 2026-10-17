# Agent Handler

提供 Agent run 创建、查询、取消和 SSE 事件接口。

- `create_run_handler.go`、`get_run_handler.go`、`cancel_run_handler.go`、`run_events_handler.go` 实现 run HTTP 处理。
- 通过 `internal/agent/chat` 服务契约操作 run；通用聊天错误与 UUID 校验复用 `utils`。
- SSE 保持事件 ID 与顺序，断开连接只关闭订阅，不取消 run。
