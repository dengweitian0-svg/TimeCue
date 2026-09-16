# internal/runtime

- 本目录负责承载应用运行单元；具体业务组件应通过明确接口接入，不得把进程级启动职责下沉到这里。
- 当前文件：
  - `runtime.go`：定义当前最小运行实现 `IdleApplication`。
- `IdleApplication.Run` 接收 bootstrap 注入的 `Store`，持续等待 `context` 取消并返回 `ctx.Err()`；`context.Canceled` 由 `bootstrap` 识别为正常退出。
- 运行单元通过 `bootstrap.Application` 接收配置、logger 和 Store；不得自行加载配置、创建全局日志器、决定进程退出码或关闭顶层资源。Store 的所有权仍归 bootstrap。
- 运行时组装边界负责创建并共享 `MutationCoordinator`、`ReminderScheduler` 与业务 `Service`，将生命周期/时间环境适配器的 `RuntimeEvent` 回流到调度器；不得绕过协调器直接修改候选、epoch 或日程事实。#14 负责在该边界接入 Wails、Windows 电源/时钟事件、真实通知和资源逆序关闭，本 Issue 不实现这些平台适配。
- 运行时的窗口可见状态与调度生命周期状态分离；`running`、`suspended`、`stopping` 只由调度契约表达。停止时先关闭业务写入和提醒派发资格，再由顶层按资源所有权完成取消和释放。
- 新增 UI、调度、存储或领域逻辑时必须拆分为职责独立的组件，通过窄接口接入运行时，并保持可测试性。
- 运行循环、状态变化、并发控制和外部副作用必须有准确的中文注释、必要日志和对应测试。
- `DesktopApplication` 只从 `bootstrap.Store` 一次性取得 `DesktopStore` 组合端口，并在运行时内共享唯一 `Service`、`MutationCoordinator`、`ReminderScheduler`；Store 的关闭责任仍归 bootstrap。
- `WailsBindings` 是平台无关的强类型边界，所有前端输出必须带 `json` tag；写入继续经过 Service/Coordinator，停止阶段返回 `RUNTIME_STOPPING`。
- 激活事件使用单调 sequence 和 `FrontendReady(last_sequence)` 握手；前端未就绪时只保留最新激活，发送失败不得静默丢弃。
- 停止必须先使 scheduler/协调器进入 `stopping`，再取消并等待 scheduler，通知清理失败进入 `cleanup_failed` 并允许 `RetryCleanup`；成功后才关闭事件源、托盘和通知资源。runtime 不关闭 bootstrap Store。
