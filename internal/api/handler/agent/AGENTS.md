# Agent Handler

提供 Agent run 创建、查询、取消、模型列表及分页/SSE 事件接口。

- `create_run_handler.go`、`get_run_handler.go`、`cancel_run_handler.go`、`run_events_handler.go` 实现 run HTTP 处理。
- 通过 `internal/agent/chat` 服务契约操作 run；通用聊天错误与 UUID 校验复用 `utils`。
- SSE 保持事件 ID 与顺序，断开连接只关闭订阅，不取消 run。

- `get_models_handler.go` 返回本地配置列表；`get_run_events_handler.go` 严格校验 after_seq 与 limit（默认 10，1–100），完整返回原始事件。创建四字段回执及概况十字段包含 provider，具体模型校验由真实服务负责。

- 用例按 ModelService、RunService、SessionService 分别注入；HTTP Handler 只使用相应窄接口，App 通过独立 Lifecycle 管理启动与停止。
- 创建 Handler 通过回执内部 NewlyCreated 元信息保持真正新建（含预算 failed）202、running 重放 202、终态重放 200；公开回执仍固定四字段。
