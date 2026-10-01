# internal/middleware

- 按一个中间件一个文件组织：`logger.go` 放 HTTPLogger 及私有日志素材，`host_guard.go` 放 HostGuard，`recovery.go` 放 Recovery 和 panic 响应辅助函数；`handler.go` 集中放 handler 包装器、结果类型、构造函数和统一响应写入，测试使用对应 `_test.go` 文件。
- `HTTPLogger()` 用于统一 HTTP 结果日志，接入时替代独立耗时日志；必须在 Host 校验和 panic 恢复之前注册。
- `HTTPHandler` 只返回 `HTTPResult`（状态、响应头、完整响应对象及原始内部 Cause），不直接写响应或日志；APIError、ErrorResponse、DataResponse 是共享协议结构，领域错误码留在 API 层。
- `WrapHTTPHandler` / `WriteHTTPResult` 是唯一的响应适配出口，自动读取公开错误及 API 层注入的 `HTTPResultFields`，再写响应；日志素材类型和设置函数只在包内可见，并保存在当前 Gin 上下文中。
- JSON 在响应头提交前序列化，序列化异常由 Recovery 返回安全的 500；响应写入失败记录原始错误和实际状态，不重写响应。
- `WriteHTTPPanic` 自动记录 panic 信息；未提交响应时写安全的 500，已提交时保留响应，最后 Abort。字段提取函数只读取结果事实，panic 由外层恢复流程处理。
- 路由注册顺序为 HTTPLogger、HostGuard、Recovery；HostGuard 的 Host 和拒绝结果由 API 层注入，中间件不得导入 API 层或硬编码其领域错误。
- 日志消息和级别统一在 middleware 匹配：panic 优先，其次原始内部错误或 5xx，再次 4xx，最后成功路由事件。创建和删除成功为 Info，查询和健康检查成功为 Debug，4xx 为 Warn，内部失败和 panic 为 Error；其他成功请求默认 Info。
- 每次请求最多输出一条 `[http]` 消息前缀的 JSONL 日志，包含 method、path、route、status、duration_ms 及结果字段；使用现有全局 logger 的格式、时间和热更新级别过滤。
- 不解析或缓存请求和响应体，不记录查询字符串及日程描述；不得依赖 API handler、服务或存储实现。
- 日志测试使用全局 logger，不得并行运行；验证单条输出、事件字段、最终状态、耗时、级别热更新及敏感内容遗漏。

- `RequestDuration` 为尚未迁移的调用方保留，只用于旧响应链路，不应和 HTTPLogger 同时注册。
