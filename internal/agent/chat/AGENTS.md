# 聊天运行

- `contracts.go` 定义聊天用例、模型和存储端口。`service.go` 管理会话、运行准入、取消和生命周期。
- `request.go` 构造模型上下文和请求，`model_retry.go` 管理模型调用、重试及恢复。
- `progress.go` 构造进度与消息快照，`event_batch.go` 管理流式事件批次，`persistence.go` 处理保存失败和终态重试。
- `tool_loop.go` 衔接模型与工具批次，`tool_attempts.go` 调度工具尝试。`tool_content.go` 和 `argument_feedback.go` 生成模型侧反馈。
- `compact.go` 管理上下文摘要，`usage.go` 定义只读模型用量用例。
- `*_test.go` 按准入、取消、模型、工具、事件、摘要、用量和持久化故障分组。

## 工程约束

- 协调器以已提交的进度和工具结果为准，保存失败时停止推进。恢复复用原调用身份及已提交事实。
- 模型侧反馈与原始工具结果独立保存。用量只读原始记录，缺失保持未知。
- 生产模型、工具和存储由 Loop 装配。恢复准备与推进分离。运行与恢复契约见[聊天说明](../../../docs/CHAT.md)。
