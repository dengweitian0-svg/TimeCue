# internal/middleware

- `handler.go` 定义 HTTPResult、HTTPHandler 及共享响应结构；结果只描述状态、响应头、完整响应对象和内部 Cause。
- 只将 Body 作为 HTTP 响应序列化；Cause 用于内部诊断，预期业务拒绝不设置 Cause，领域错误码留在 API 层。
- JSONResult 使用完整响应对象，DataResult 包装 data，ErrorResult 包装公开 error，NoContentResult 表示无响应体的 204。
- `RequestDuration` 仍用于既有请求链路，记录独立的 Info 耗时日志。
