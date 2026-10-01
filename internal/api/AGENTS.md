# internal/api

- `router.go` 只创建 Gin 路由、校验 Host、统一处理未匹配路由和方法，并建立唯一的 `scheduler` 分组。
- HTTP 监听地址固定为 `127.0.0.1:10086`；仅接受对应 Host。健康检查路径为 `/health`，日程路径前缀为 `/api/v1/scheduler`。
- 每个 HTTP 接口的处理函数独立存放在 `handler` 中的一个文件；请求与响应结构留在对应处理文件内。
- handler 只调用服务层日程用例，不直接读写 SQLite。数据库错误详情只进日志，HTTP 使用稳定错误码。
- 成功响应统一使用 `data`；`handler/error.go` 集中定义各接口共用的错误类型、错误码及消息，并负责普通错误输出。其他 handler 引用这些定义；健康检查失败使用同一错误内容类型，放在 `data.error` 中，并设置 `data.status=error`。
- 创建输入拒绝未知字段和超过 1 MiB 的请求体；响应时间固定为 `+08:00`。
- 创建请求中的 `single` 与 `period` 使用可选 JSON tag，按 `type` 要求恰好出现对应规则；未使用的规则字段必须省略。
- handler 中的结构体实例化按字段分多行书写，嵌套响应也保持同样格式。
- handler 在关键结果写结构化日志：创建和软删除成功记 `Info`，查询及健康检查成功记 `Debug`，4xx 校验或路由拒绝由 `WriteError` 统一记 `Warn`，内部失败记录原始错误。日志不包含请求体和日程描述。
