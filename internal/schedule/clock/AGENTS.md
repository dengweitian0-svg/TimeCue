# internal/schedule/clock

- 本目录提供时间环境适配器，将操作系统时间和本地时区转换为 `schedule/domain.TimeSnapshot`。
- 当前文件：
  - `clock.go`：`SystemClock` 实现 `app.Clock` 所需的快照能力。
  - `clock_test.go`：验证系统时间环境和取消上下文行为。
- `SystemClock.Snapshot` 必须先检查 `context.Context` 是否已取消，再读取系统时钟；一次调用返回一个可供整次用例复用的时间和时区快照。
- 领域规则不得直接依赖本目录；本目录也不得实现日程校验、持久化、日志策略或进程生命周期。
- 新增时钟实现（例如测试时钟或外部时间源）必须保持 `Clock` 契约，并明确取消、失败和时区语义；修改后同步更新测试。
