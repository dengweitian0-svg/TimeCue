# HTTP Utilities

提供健康检查、通用错误响应、严格 JSON 解析和聊天接口共享适配。

- 不持有业务状态，不依赖兄弟 Handler 包。
- 运行信息只读引用 `internal/app` 的公开类型。
- Host 拒绝错误使用当前路由端口生成准确提示。
- 保持既有错误码、请求体大小限制与字段校验行为。

- 聊天配置不可用映射 503 model_config_unavailable；未配置组合为 400 invalid_request；独立 error/end 由真实聊天服务提供，不保留旧 Mock 的连接占用/内存缓冲错误。
