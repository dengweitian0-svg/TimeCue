# HTTP API

提供本机 HTTP 路由与业务接口装配。

提醒视图 deliveryStatus 使用 pending/loaded/sent/failed；loaded 表示加载/等待执行，不能当作展示成功。待确认查询仍按确认时间筛选，确认不改变投递状态。枚举扩展须完成 Apifox 验收后再接前端。

- `router.go`、`chat_routes.go`：注册健康检查、日程/提醒路由；唯一装配入口 `NewRouter` 从 `App.ChatService` 注册可选 `/v1` 聊天路由。
- [handler](handler/AGENTS.md)：请求解析与校验、服务调用、响应 DTO 和业务错误映射。
- `router_test.go`：验证路由、输入校验、响应协议和 HTTP 日志。
- `health_runtime_test.go`：健康成功及两类失败的运行身份、PID、版本、能力和 HostGuard 回归。
- `delivery_test.go`：投递接口严格输入、空队列、当前运行身份、退出 attempt 精确匹配、迟到退出及结果回流和日志隐私测试。
- `recovery_test.go`：真实 SQLite 的恢复摘要、任务及 attempt 成员查询、参数组合、旧身份和投递/确认独立性 HTTP 回归。
- `schedule_logging_test.go`：使用真实 SQLite 验证创建、更新、启停、删除、幂等重放及提交后响应失败的日志；`http_log_helpers_test.go` 复用 API 路由装配，提供日志捕获、断言和响应短写模拟。

工程约束：

- 聊天 API 仅提供创建/查询/取消 run、run SSE、查询/列出/删除 session 七个接口；Handler 只校验输入、调用 `internal/agent/chat` 契约并构造 HTTPResult。聊天服务端口由 `App.ChatService` 持有，路由装配不构造服务或 Mock。
- 聊天路由使用 `/v1` 前缀，成功日志只提取 ID、状态和数量；SSE 通过统一流式响应出口逐事件刷新并关闭订阅。

- NewRouter 可选注入 RuntimeInfo，保留原两参数调用及 nil App 健康检查测试。生产入口注入的运行信息在 `/health` 成功和失败中一致，沿用 200/503、data.error、错误码及 healthFields；不增加停止路由。

- 新增或修改路由时，同步 `router.go` 的 handler/`HTTPResultFields` 注册和 [middleware/logger.go](../middleware/logger.go) 的 `httpEvents`；事件键使用 HTTP 方法与包含分组前缀的完整路由模板。日程创建/更新/删除由 handler 附加正式操作摘要，注册时无需再从响应 DTO 提取。
- `WrapHTTPHandler(handler, fields)` 的字段提取函数读取明确响应 DTO；先判断响应状态，失败不提取成功字段。健康检查失败可提取公开错误码。已提交的日程变更通过 `HTTPResult.LogFields` 独立传递，响应失败仍须保留。
- DTO 或响应分支变化时同步字段提取函数；不解析请求/响应 JSON，不记录名称、说明或完整批量 ID 集合。
- 中间件顺序为 `HTTPLogger` → `HostGuard` → `Recovery`。路由测试覆盖事件消息、日志级别、业务字段及失败时的字段遗漏。
- 日程变更日志的完整链路测试归 API；日志素材保留、序列化失败、panic 和请求隔离等通用测试归 middleware。使用全局日志器的测试不并行执行。
- POST notifications/claim 领取完整任务，delivery-attempts/:id/start 冻结执行决定，delivery-attempts/:id/results 回填 sent/failed；只支持单执行者。不存在或旧进程身份返回 404，未开始回填及矛盾结果返回 409，停止接纳返回 503。
- POST notifications/executor-exit 要求 instanceId 与 attemptIds；当前运行身份匹配后只整理列表中仍活动的尝试，未知 ID 忽略，空列表无操作。未知平台结果不伪造失败且不重置已用次数；迟到的真实结果仍可回填。JSON 体沿用 1 MiB 上限。
- 投递路由由 handler 直接附加 HTTPResult.LogFields，包含运行身份、数量和次数，不包含文案及成员列表；响应失败仍保留已提交操作事实。
- 领取和开始支持 minute/recovery；recovery 全部整理完成才开放，开始文案仅含实际数量。GET notifications 通过 deliveryTaskId 及可选 attemptId 查询当前任务历史或冻结成员；与 state 模式互斥，未开始尝试及成员版本变化返回 409，旧身份返回 404。
- 组合快照增加 recovery（无轮次为 null），recovery 任务分页附加对应轮次进度；未知 missedTotal 为 null，unacknowledgedCount 返回本轮已整理历史中尚未确认的数量，与完整遗漏及可投递统计独立；确认后可归零而历史仍保留。原 total 与 openScheduleCount 的统计范围不变。查询日志只追加恢复身份、阶段和已整理数，不记录文案、游标或成员集合。

- `schedule_change_test.go`：原子暂停创建、分钟精度、版本冲突、类型边界、创建/更新幂等重放和已删除规则拒绝更新。

日期视图及组合快照的 occurrences 页增加 openScheduleCount，表示全部分页中的当前启用日程数（按 Schedule ID 去重，不筛确认状态）；提醒待确认页不携带此字段。路由和日志字段提取的响应类型不变。

待确认查询支持可选 scope=today/history，按原计划时间及固定 UTC+8 零点分类；缺省仍读取全部到期未确认记录。范围总数先于分页，跨范围及跨北京日期游标返回 invalid_request 400；不改写确认、投递或父日程事实。unacknowledged_scope_test.go 验证参数、旧查询兼容和游标隔离。
