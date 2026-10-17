# Session Handler

提供会话查询与删除接口；查询返回 session 名称及 run 列表。

- `get_session_handler.go`、`list_sessions_handler.go`、`delete_session_handler.go` 实现会话 HTTP 处理。
- 通过 `internal/agent/chat` 服务契约访问会话，UUID 校验与聊天错误映射复用 `utils`。
- 保持现有 DTO、状态码和错误响应。
