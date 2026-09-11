# internal/runtime

- 本目录负责承载应用运行单元；具体业务组件应通过明确接口接入，不得把进程级启动职责下沉到这里。
- 当前文件：
  - `runtime.go`：定义当前最小运行实现 `IdleApplication`。
- `IdleApplication.Run` 持续等待 `context` 取消并返回 `ctx.Err()`；`context.Canceled` 由 `bootstrap` 识别为正常退出。
- 运行单元通过 `bootstrap.Application` 接收配置和 logger；不得自行加载配置、创建全局日志器、决定进程退出码或关闭顶层资源。
- 新增 UI、调度、存储或领域逻辑时必须拆分为职责独立的组件，通过窄接口接入运行时，并保持可测试性。
- 运行循环、状态变化、并发控制和外部副作用必须有准确的中文注释、必要日志和对应测试。
