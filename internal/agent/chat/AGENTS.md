# 聊天契约和进度

- `contracts.go` 定义聊天用例、模型和存储端口，`usage.go` 定义只读模型用量的返回值和存储端口。
- `context_builder.go` 组装有效历史和模型消息，沿实际请求前缀恢复续写正文。预算以 `budget.go` 的字节估算为准。
- `compact.go` 提取待压缩历史、构造无工具摘要请求并校验正文。
- `progress.go` 生成可保存的进度快照。`tool_content.go` 和 `argument_feedback.go` 生成模型侧反馈，原始结果单独保存。
- SQLite v3 checkpoint 格式及既有提示的读取规则见[聊天说明](../../../docs/CHAT.md)。
