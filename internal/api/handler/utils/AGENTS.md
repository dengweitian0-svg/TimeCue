# HTTP Utilities

提供健康检查、通用错误响应、严格 JSON 解析和聊天接口共享适配。

- 不持有业务状态，不依赖兄弟 Handler 包。
- 运行信息只读引用 `internal/app` 的公开类型。
- Host 拒绝错误使用当前路由端口生成准确提示。
- 保持既有错误码、请求体大小限制与字段校验行为。

- 聊天配置不可用映射 503 model_config_unavailable；未配置组合为 400 invalid_request；独立 error/end 由真实聊天服务提供，不保留旧 Mock 的连接占用/内存缓冲错误。
- 运行概况允许 cancelled 且 finished_at=null 的取消收尾投影；succeeded/failed 仍必须带结束时间。公开 cancelled 状态本身不证明运行已经结束。
- 配置 503 使用中文提示检查模型预算及实例 .env，不包含密钥；上游模型错误在已接受 run 的概况与事件中反馈，不直接改成本机 HTTP 状态。
