# Agent 应用层

- `chat`：会话、运行、创建幂等、准入、查询、协作取消、订阅及持久化窄端口。
- `completion`：Chat Completions HTTP/SSE 模型适配器，解析真实正文、思考、响应 ID 和原始 usage。

应用层不依赖 App、HTTP Handler 或具体 SQLite。正常与开发入口均装配真实服务，无内存聊天产品实现或种子会话。测试仅在上游响应和故障边界注入替身。
