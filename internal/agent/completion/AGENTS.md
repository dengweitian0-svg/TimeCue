# Chat Completions 适配器

- `adapter.go` 生成预算、SQLite 和 SDK 共用的最终 Chat Completions 正文。
- `client.go` 使用 SDK 原样发送已准备的正文，转换 SSE 增量、用量和上游错误。
- `client_test.go` 覆盖 HTTP/SSE 协议、超时、断流和脱敏。

## 工程约束

- 密钥仅随鉴权发送，日志和错误消息须脱敏。模型重试由聊天协调器负责。
