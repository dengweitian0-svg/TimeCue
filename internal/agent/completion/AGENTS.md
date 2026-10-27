# Chat Completions 适配器

- `client.go`：官方 openai-go/v3 SDK 的流式调用与响应转换、reasoning_content/content、响应 ID、finish_reason 与原始 usage；上游错误映射及首次有效输出、空闲超时。
- `client_test.go`：真实 HTTP/SSE 验证七类上游错误及脱敏、未分类 HTTP 不冒充可重试 500、保活不延长期限、首次/空闲及传输超时、断流部分正文和原始 usage。

密钥只放 Authorization，不写入模型 input、错误消息或日志；默认禁止重定向。请求取消及时关闭上游连接并返回实际部分结果。保活不算有效输出。T01 不提供工具定义；非 stop 完成或意外工具输出如实失败，不执行业务。

意外工具与正文/思考出现在同一流块时，先保留本块已收到文字及用量，再按 model_invalid_format 失败，不执行工具或重试。

SDK 的隐式重试显式关闭，重试计数属于项目运行协调器；发送的 assistant.reasoning_content 保持实际快照原文。响应 reasoning_content 通过 SDK 的扩展字段读取，原始 usage JSON 不重新估算。

发送时通过 SDK 的 WithRequestBody 使用 ModelRequest 的完整 JSON 序列化结果，保持 UTF-8、转义及结构开销与预算检查、持久化 input 字节一致。实际快照中的 assistant.tool_calls 与 tool_call_id 配对结果原样发送；这只提供历史消息接续，不启用工具定义或业务执行。

首次/空闲期限在单次请求期间固定，由有效正文或思考重置；空 delta、响应 ID、usage 与 keep-alive 不算有效输出。期限回调与有效输出串行核验，关闭请求时停止计时；本地取消不接纳后续流块。错误消息由本地固定文案生成，不回显上游错误正文；未分类 HTTP 使用 model_invalid_format，不自动按服务器暂时故障重试。
