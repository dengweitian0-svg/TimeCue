# Chat Completions 适配器

- `client.go`：官方 openai-go/v3 SDK 的流式调用与响应转换、reasoning_content/content、响应 ID、finish_reason 与原始 usage；上游错误映射及首次有效输出、空闲超时。

密钥只放 Authorization，不写入模型 input、错误消息或日志；默认禁止重定向。请求取消及时关闭上游连接并返回实际部分结果。保活不算有效输出。T01 不提供工具定义；非 stop 完成或意外工具输出如实失败，不执行业务。

SDK 的隐式重试显式关闭，重试计数属于项目运行协调器；SDK 使用标准参数，assistant.reasoning_content 通过 SetExtraFields 注入；实际请求与请求前保存的 input 保持一致。reasoning_content 通过 SDK 的扩展字段读取，原始 usage JSON 不重新估算。
