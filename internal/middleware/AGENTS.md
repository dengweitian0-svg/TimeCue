# HTTP 中间件

提供 HTTP 请求守卫、响应适配与日志记录。

- `handler.go`：处理函数包装、共享响应结构、JSON 序列化与响应写入。
- `logger.go`：请求耗时、状态、业务事件字段和日志级别匹配。
- `host_guard.go`：检查请求 Host 并构造拒绝响应。
- `recovery.go`：捕获 panic，处理异常响应与请求终止。
- 对应测试文件验证响应失败、异常恢复和请求日志行为。

工程约束：

- `logger.go` 的 `httpEvents` 维护方法/完整路由模板对应的成功级别和成功/失败消息；响应业务字段由 API 注册的 `HTTPResultFields` 提取，中间件不导入 API、service 或 store。
- `WrapHTTPHandler`/`WriteHTTPResult` 统一写响应并收集日志素材；字段函数不写响应或日志。JSON 在提交响应前序列化，已提交响应遇到写入错误或 panic 时保留实际状态和原始原因。
- `HTTPLogger` 在 Host 校验和异常恢复前注册，每请求最多一条 `[http]` 日志。查询/健康检查成功为 Debug，创建/更新/删除/确认为 Info，4xx 为 Warn，内部错误/panic 为 Error。
- 日志不记录请求/响应体、查询字符串或日程说明；使用全局 logger 的测试不并行运行。

阶段 C 同步 PUT 日程更新的 Info 事件及失败消息；日志测试使用 001 + 002 迁移，失败存储替身实现正式规则变更端口。
