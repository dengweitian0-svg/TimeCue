# HTTP 中间件

- `handler.go` 包装处理函数并写入 JSON 或流式响应，`stream.go` 定义 SSE 写入。
- `logger.go` 记录请求结果和业务事件，`host_guard.go` 校验 Host，`recovery.go` 处理 panic。
- `*_test.go` 覆盖响应、流式写入、异常和请求日志。

## 工程约束

- 中间件统一写响应和请求日志。日志禁止记录请求或响应体、查询字符串及密钥。
