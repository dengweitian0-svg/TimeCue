# API Handler

按 HTTP 功能拆分请求处理包：`session` 处理会话，`agent` 处理 run、模型与事件分页/SSE，`schedule` 处理日程、提醒和投递，`utils` 提供健康检查与通用 HTTP 适配。

- Handler 解析并校验 HTTP 输入，调用应用或聊天服务契约，返回 `middleware.HTTPResult`。
- 不直接访问数据库、写响应或写日志；保持现有请求、响应、错误码和 SSE 语义。
- 每个接口处理函数独立成文件，导出函数使用中文注释。
