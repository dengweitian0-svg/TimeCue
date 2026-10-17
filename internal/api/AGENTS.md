# HTTP API

提供本机 HTTP 路由注册和 API 集成测试。

- `router.go` 注册健康、日程、提醒和投递接口，按入口指定端口限制 loopback Host，并装配 HTTP 日志字段。
- `chat_routes.go` 注册可选的 `/v1` run、SSE 和 session 路由。
- [handler](handler/AGENTS.md) 按 session、agent、schedule、utils 拆分请求处理。
- [test](test/AGENTS.md) 保存 API 路由与 HTTP 契约测试。

约束：路由保持现有路径、JSON、状态码、错误码、日志和 SSE 语义；NewRouter 默认校验 127.0.0.1:10086，NewRouterWithPort 校验传入端口；健康检查允许在 App 为空时使用，并可接收只读运行信息。
