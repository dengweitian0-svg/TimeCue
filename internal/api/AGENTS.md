# HTTP API

- `router.go` 注册健康、日程、提醒和投递路由，装配本机 Host 校验及日志。日程和聊天路由分别注入 App 与 Loop 的用例端口。
- `chat_routes.go` 注册聊天运行、模型、事件和会话路由。
- [handler](handler/AGENTS.md) 提供请求处理器。
- [test](test/AGENTS.md) 提供 HTTP 契约回归。
