# HTTP API

提供本机 HTTP 路由与业务接口装配。

- `router.go`：注册健康检查、日程创建/更新/启停/删除及提醒路由，装配中间件、404/405 响应及请求日志字段提取。
- [handler](handler/AGENTS.md)：请求解析与校验、服务调用、响应 DTO 和业务错误映射。
- `router_test.go`：验证路由、输入校验、响应协议和 HTTP 日志。

工程约束：

- 新增或修改路由时，同步 `router.go` 的 handler/`HTTPResultFields` 注册和 [middleware/logger.go](../middleware/logger.go) 的 `httpEvents`；事件键使用 HTTP 方法与包含分组前缀的完整路由模板。
- `WrapHTTPHandler(handler, fields)` 的字段提取函数读取明确响应 DTO；先判断响应状态，失败不提取成功字段。无响应体的删除接口可读取已校验的路径 ID；健康检查失败可提取公开错误码。
- DTO 或响应分支变化时同步字段提取函数；不解析请求/响应 JSON，不记录名称、说明或完整批量 ID 集合。
- 中间件顺序为 `HTTPLogger` → `HostGuard` → `Recovery`。路由测试覆盖事件消息、日志级别、业务字段及失败时的字段遗漏。

- `schedule_change_test.go`：原子暂停创建、分钟精度、版本冲突、类型边界、创建/更新幂等重放和已删除规则拒绝更新。