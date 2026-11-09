# Agent 请求处理

- `{create,get,cancel}_run_handler.go` 处理运行创建、查询和取消。
- `get_run_events_handler.go` 和 `run_events_handler.go` 分别处理事件分页和 SSE。
- `get_models_handler.go` 处理模型列表，`chat_http_helpers.go` 提供本包共享适配。

## 工程约束

- Handler 通过聊天用例读取和操作运行，SSE 断连只结束订阅。
