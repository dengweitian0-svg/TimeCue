# API 测试

- 本目录包含 API 路由、HTTP、SSE、日志、恢复和投递协议测试，使用外部测试包 `api_test`。
- 通过 `api.NewRouter` 验证默认端口公开 HTTP 行为，并覆盖 `NewRouterWithPort` 的自定义 Host 校验；共享 HTTP 测试辅助函数保留在对应测试文件中。
- SQL 迁移文件从本目录以 `../../../migrations` 相对路径读取。
- 日志捕获测试需串行运行，避免全局日志配置相互影响。

- `chat_test.go`：真实聊天服务、临时 SQLite 与上游响应替身驱动的 HTTP 契约回归；验证 provider、四/十字段 DTO、幂等重放、模型列表、事件分页、SSE 终态、超前 Last-Event-ID 返回 400 invalid_request 及软删除；不再人工发布事件或确认终态。
- `chat_test.go` 验证预算拒绝首次创建 202、终态原回执重放 200、固定四字段 JSON、保留真实输入和完成时间，且不请求上游。
- `chat_admission_test.go`：真实 SQLite、公开 HTTP 和取消终态事务故障边界验证 202 四字段回执、重放先于模型与并发门禁、同键冲突、会话/全局 409、非法请求不登记键、被拒绝键重用，以及取消投影 200、十字段概况与空 finished_at 期间继续占用。

聊天 HTTP 契约隔离测试显式为 NewService 传入 nil Registry；真实注册工具、日程查询及回填由 internal/agent/chat 的公共用例集成测试覆盖。

- `chat_admission_test.go` 验证取消等待中的 POST cancel 直接返回 200/cancelled，并记录 Info 的 run cancellation requested；公开服务集成中的活动会话删除验证位于 chat/cancel_test.go，覆盖全部新查询不可见及原 SSE 收尾。
- 准入测试等待取消运行的 finished_at 后再复用名额，不把取消受理回执当作已释放。
