# Chat Completions 适配器

- `client.go` 发送 Chat Completions 请求，转换 SSE 增量、用量和上游错误。
- `client_test.go` 覆盖 HTTP/SSE 协议、超时、断流和脱敏。

## 工程约束

- 密钥仅随鉴权发送，日志和错误消息须脱敏。模型重试由聊天协调器负责。
