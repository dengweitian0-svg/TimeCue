# HTTP API

提供本机 HTTP 路由注册和 API 集成测试。

- `router.go` 注册健康、日程、提醒和投递接口，按入口指定端口限制 loopback Host，并装配 HTTP 日志字段。
- `chat_routes.go` 注册可选的 `/v1` run、事件分页、模型、SSE 和 session 路由。
- [handler](handler/AGENTS.md) 按 session、agent、schedule、utils 拆分请求处理。
- [test](test/AGENTS.md) 保存 API 路由与 HTTP 契约测试。

约束：路由保持现有路径、JSON、状态码、错误码、日志和 SSE 语义；NewRouter 默认校验 127.0.0.1:10086，NewRouterWithPort 校验传入端口；健康检查允许在 App 为空时使用，并可接收只读运行信息。

- T01 创建必填 provider，返回四字段回执（running 202、终态重放 200）；概况固定十字段并保留失败部分 output；/v1/model 返回 provider/model/available；/events 与 SSE 共用原始序号。

- 用例按 ModelService、RunService、SessionService 分别注入；HTTP Handler 只使用相应窄接口，App 通过独立 Lifecycle 管理启动与停止。
