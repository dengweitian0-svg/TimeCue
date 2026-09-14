# internal/runtime

- 本目录负责承载应用运行单元；具体业务组件应通过明确接口接入，不得把进程级启动职责下沉到这里。
- 当前文件：
  - `runtime.go`：定义当前最小运行实现 `IdleApplication`。
- `IdleApplication.Run` 持续等待 `context` 取消并返回 `ctx.Err()`；`context.Canceled` 由 `bootstrap` 识别为正常退出。
- 运行单元通过 `bootstrap.Application` 接收配置和 logger；不得自行加载配置、创建全局日志器、决定进程退出码或关闭顶层资源。
- 运行时组装边界负责创建并共享 `MutationCoordinator`、`ReminderScheduler` 与业务 `Service`，将生命周期/时间环境适配器的 `RuntimeEvent` 回流到调度器；不得绕过协调器直接修改候选、epoch 或日程事实。#14 负责在该边界接入 Wails、Windows 电源/时钟事件、真实通知和资源逆序关闭，本 Issue 不实现这些平台适配。
- 运行时的窗口可见状态与调度生命周期状态分离；`running`、`suspended`、`stopping` 只由调度契约表达。停止时先关闭业务写入和提醒派发资格，再由顶层按资源所有权完成取消和释放。
- 新增 UI、调度、存储或领域逻辑时必须拆分为职责独立的组件，通过窄接口接入运行时，并保持可测试性。
- 运行循环、状态变化、并发控制和外部副作用必须有准确的中文注释、必要日志和对应测试。
