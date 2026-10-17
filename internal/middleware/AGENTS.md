# HTTP 中间件

提供 HTTP 请求守卫、响应适配与日志记录。

- `handler.go`：处理函数包装、共享响应结构、JSON/流式响应序列化与统一写入。
- `stream.go`：流式响应窄接口及 SSE 帧编码。
- `logger.go`：请求耗时、状态、业务事件字段和日志级别匹配；执行者退出按正式事件记录身份与尝试数量，不记录尝试 ID 集合。
- `host_guard.go`：检查请求 Host 并构造拒绝响应。
- `recovery.go`：捕获 panic，处理异常响应与请求终止。
- 对应测试文件验证响应失败、异常恢复和请求日志行为。

工程约束：

- `logger.go` 的 `httpEvents` 维护方法/完整路由模板对应的成功级别和成功/失败消息；响应业务字段由 API 注册的 `HTTPResultFields` 提取，已完成操作事实由 `HTTPResult.LogFields` 提供，中间件不导入 API、service 或 store。
- `WrapHTTPHandler`/`WriteHTTPResult` 统一写响应并收集日志素材；字段函数不写响应或日志。`LogFields` 在响应校验和 JSON 序列化前保存，序列化/提取/panic/写入失败不丢失已提交的变更事实。JSON 在提交响应前序列化，已提交响应遇到写入错误或 panic 时保留实际状态和原始原因。
- `HTTPResult.SuccessMessage` 仅覆盖正常完成时的成功消息，幂等重放使用 `schedule write replayed`；内部错误、panic 和 4xx 始终优先使用失败消息及对应级别。内部素材仅用于日志，不进入响应 JSON。
- `HTTPLogger` 在 Host 校验和异常恢复前注册，每请求最多一条 `[http]` 日志。查询/健康检查成功为 Debug，创建/更新/删除/确认为 Info，4xx 为 Warn，内部错误/panic 为 Error。
- `WriteHTTPResult` 管理 Stream 的写入和关闭；SSE 事件逐条刷新，流式请求清除服务器写超时。流式响应错误仍由统一 HTTP 日志记录；成功响应可用 `LogLevel` 覆盖默认级别。
- SSE 事件仅允许 `start`、`thinking`、`text`、`tool`、`end`，外层 ID 由调用方提供，data 必须 JSON 编码。
- 日志不记录请求/响应体、查询字符串或日程说明；使用全局 logger 的测试不并行运行。
- 投递 claim 成功使用 Debug，start/results 使用 Info；事件覆盖包含分组前缀的完整路由模板，运行身份、数量、次数及结果来自 handler 的 LogFields，不记录展示文案。

`logger_test.go` 的测试装配仅依赖 App 门面与 schedule/notification Repo；生产中间件不导入这些业务模块。查询失败测试替身同时实现范围读取端口，保留原始数据库错误供 HTTP 日志断言。
