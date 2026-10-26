# API 测试

- 本目录包含 API 路由、HTTP、SSE、日志、恢复和投递协议测试，使用外部测试包 `api_test`。
- 通过 `api.NewRouter` 验证默认端口公开 HTTP 行为，并覆盖 `NewRouterWithPort` 的自定义 Host 校验；共享 HTTP 测试辅助函数保留在对应测试文件中。
- SQL 迁移文件从本目录以 `../../../migrations` 相对路径读取。
- 日志捕获测试需串行运行，避免全局日志配置相互影响。

- `chat_test.go`：真实聊天服务、临时 SQLite 与上游响应替身驱动的 HTTP 契约回归；验证 provider、四/十字段 DTO、幂等重放、模型列表、事件分页、SSE 终态及软删除；不再人工发布事件或确认终态。
