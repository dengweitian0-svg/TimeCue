# Chat 模块

- `contracts.go`：固定四字段回执、十字段概况、模型选项、分页事件、内外状态以及模型端口、运行、会话、模型执行持久化和事件查询四个独立存储接口。
- `service.go`：真实运行协调器；持久化幂等优先于配置门禁，同会话互斥、全局最多三会话并发；读取本地模型选项、处理无在途工具的取消、会话隐藏和单连接替换。
- `service_test.go`：通过公共 RunService/SessionService/ModelService、真实临时 SQLite 和可控上游 HTTP 验证流式持久化、重启、预算、取消及幂等。

请求实际 input、running 和 checkpoint 提交后才发送上游请求；结果与原始 usage、进度、终态原子提交。SSE 和分页读取相同持久化序号，断开只影响订阅。正常退出保存部分 output，保留 running 与原 checkpoint；启动先重建占用，再沿同一逻辑调用和原 input 后台重发。

缺少配置或 Key 不阻断启动与本地查询；拒绝真正新建，不回退固定回复。运行资源不依赖 HTTP 请求生命周期。内部 wait_for_cancel 与对外 cancelled 分开建模；T01 只验收模型取消。工具执行、网络退避重试、自动续写和摘要由后续切片扩展；max_retry 首版登记而不执行网络重试。

- 用例按 ModelService、RunService、SessionService 分别注入；HTTP Handler 只使用相应窄接口，App 通过独立 Lifecycle 管理启动与停止。

- 终态保存失败后保留占用，按五秒间隔最多重试三次，仅重试持久化、不重发模型；耗尽释放资源但不伪造已落库终态。
