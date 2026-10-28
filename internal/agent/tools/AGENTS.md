# 工具注册与日程业务

- `registry.go`：统一 `Tool.Info` / `Tool.InvokableRun` 契约、`ToolInfo` 元数据、显式注册、模型定义和对象参数 schema 校验；支持字符串、整数、布尔与字符串数组，拒绝重名和不支持的定义，执行入口统一处理解析、校验、错误及 JSON 结果。
- `schedule.go`：`get_schedule`、`list_schedules`、`query_occurrences` 三个具体工具，分别提供定义并实现同步调用；入口通过 `ScheduleTools` 和 `Registry.Register` 注册，依赖 `ScheduleQueries` 正式窄端口。

- `schedule_write.go`：`create_schedule` / `update_schedule` 通过 `ScheduleWrites` 调用 App 正式回读和 WriteSchedule；参数使用平铺 name/description/notifyTime/cron/open，创建还需 type，修改必填 id/expectedRevision 并保留未提交字段。结果保留完整规则、revision 和正式提醒变更摘要。
- `time.go`：解析 RFC3339、北京时间 YYYY-MM-DD HH:mm 及 今天/明天/后天/昨天 HH:mm；相对词每次解析才读时钟，不使用运行创建时间，不补默认日期或时刻，秒精度仍由正式规则拒绝。

应用只装配一个统一 Registry，所有工具都必须注册后才能供模型使用和执行。注册在服务启动前完成，运行时只读；名称、说明、schema、dangerous 与 parallel 均由后端代码固定，不能由模型覆写。三个查询工具非危险、可并行；创建/修改非危险、串行、空业务 key，工具键 SQL NULL，不按名称或时间去重。不访问 SQLite 或 HTTP Handler，不拼装 checkpoint；开始、回填及异常配对沿统一聊天进度契约执行。

参数校验在工具开始事务提交后进行，原始 arguments 不改写。语法错误与合法非对象/schema 错误分别返回 tool_argument_parse_error 和 tool_argument_validation_error；不猜测参数，不执行业务。统一 JSON 字符串调用返回合法 JSON，注册中心直接保留对象、数组、标量及 null 类型，不额外编码为字符串；日程错误在具体工具中转换为公开错误，未知错误不回显内部细节。

单条及列表查询保留正式规则字段和 revision；列表默认 50、最大 50，支持 offset/name/type。日期查询复用正式发生情况页及 nextCursor，相对日期在每次实际解析时按北京时间计算。自动化通过 chat 公共用例、真实模型适配器、SQLite 与正式日程入口验证。

写入复用正式规则校验、版本检查和提醒联动，版本冲突返回 revision_conflict、不存在返回 schedule_not_found、规则拒绝返回 invalid_schedule，不自动重试。缺信息或多目标先澄清，明确完整且唯一时直接执行；批量独立成功/失败，依赖操作分模型轮次。已保存明确时间和实际结果沿原进度复用，不承诺无键写入恰好执行一次；完整写入恢复由后续切片交付。
