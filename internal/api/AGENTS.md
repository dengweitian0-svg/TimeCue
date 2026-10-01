# internal/api

- `router.go` 创建 Gin 路由、校验 Host、统一处理未匹配路由和方法，并建立唯一的 `scheduler` 分组；通过 `WrapHTTPHandler` 注册返回结果的 handler，并集中定义只读取明确 DTO 的日志字段提取规则。
- HTTP 监听地址固定为 `127.0.0.1:10086`；仅接受对应 Host。健康检查路径为 `/health`，日程路径前缀为 `/api/v1/scheduler`。
- 每个 HTTP 接口的处理函数独立存放在 `handler` 中的一个文件；请求与响应结构留在对应处理文件内。
- handler 只调用服务层日程用例，不直接读写 SQLite。数据库错误详情只进日志，HTTP 使用稳定错误码。
- 成功响应统一使用 `data`；`handler/error.go` 使用 middleware 的共享 `APIError` 类型，集中定义领域错误码、消息及错误结果构造。普通错误使用顶层 `error`；健康检查失败使用同一错误内容类型，放在 `data.error` 中，并设置 `data.status=error`。
- 创建输入拒绝未知字段和超过 1 MiB 的请求体；响应时间固定为 `+08:00`。
- 创建请求中的 `single` 与 `period` 使用可选 JSON tag，按 `type` 要求恰好出现对应规则；未使用的规则字段必须省略。
- handler 中的结构体实例化按字段分多行书写，嵌套响应也保持同样格式。
- handler 返回 `middleware.HTTPResult`，只读取请求、校验输入和调用服务，不直接写响应或日志，不导入 `log/slog`；原始内部错误放在 `Cause`，预期的 4xx 拒绝不设置 `Cause`。参数校验辅助函数返回值及可选的失败结果，不写响应。
- `ScheduleResponse`、`HealthData` 是字段提取所需的响应 DTO；`ScheduleListResponse` 的实际 Limit/Offset 使用 `json:"-"`，只返回结果事实，客户端仍接收原有数组结构。
- 响应由 middleware 的 `WriteHTTPResult` 统一写入并自动收集日志素材；router 注册独立文件内的 HostGuard 和 Recovery，注入 Host 和拒绝结果；404/405 使用包装函数。
- `router.go` 在 Host 校验与 panic 恢复之前注册唯一的 `HTTPLogger` 日志 middleware；恢复逻辑仍负责响应与终止请求，已经写出响应时保留实际状态。
- HTTP 日志由 middleware 每次请求最多输出一条：创建和软删除成功记 `Info`，查询及健康检查成功记 `Debug`，4xx 记 `Warn`，内部失败及 panic 记 `Error`。日志不包含请求体、响应体、查询字符串和日程描述。
