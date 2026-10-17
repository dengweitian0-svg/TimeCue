# Chat 模块

- `contracts.go`：聊天 run、session 受控视图、事件、终态和应用服务接口。
- `mock.go`：API 验收期间使用的临时内存服务，提供固定流式演示、事件回放、取消和 session 删除模拟。
- `mock_test.go`：验证 Mock 的事件顺序、终态、session 查询和可见性。

约束：Mock 不访问数据库、模型或真实 Agent loop；模型回复是固定内容，session 名称默认空字符串，不能作为真实业务能力的验收依据。HTTP 层只依赖 `Service` 契约，事件 ID 与事件数据由应用服务提供。请求格式校验由 API 层负责。
