# Agent

- [loop](loop/AGENTS.md) 持有唯一 Agent 实例，协调运行、恢复、会话、查询、订阅和宿主生命周期。
- [chat](chat/AGENTS.md) 定义聊天端口和数据，组装请求上下文，并生成持久化进度。
- [completion](completion/AGENTS.md) 适配 Chat Completions。
- [tools](tools/AGENTS.md) 注册、调度和执行工具，并接入正式业务入口。
- `retry.go` 提供模型及工具共用的指数退避计算。
