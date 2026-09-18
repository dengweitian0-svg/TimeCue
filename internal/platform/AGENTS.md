# internal/platform

- 本目录根包只定义桌面边界的跨平台窄契约和测试 fake，不实现 Wails、Win32、Toast、托盘、配置或 Runtime 组装；具体目录文件系统适配放在 `filesystem` 子包。
- `DesktopHost` 负责主循环和退出请求；`WindowController` 只负责窗口动作；`FrontendEvents` 只发送受控边界事件。
- `FrontendEventRecovery` 只承载 `app.RecoveryEvent` 的稳定状态和错误分类；平台适配器不得把 SQLite 错误、通知正文或底层 cause 放入恢复事件。
- `DirectoryAccess` 负责验证、创建和规范化用户目录；`DirectoryChooser` 只负责打开平台目录选择器，取消以空路径表示。具体文件系统实现位于 `platform/filesystem`，Wails 对话框适配位于 `platform/wailshost`；Runtime 通过窄端口使用它们。
- `ActivationSource` 和 `EnvironmentEventSource` 只能把外部事实作为事件输出。激活目标复用 `schedule/domain.OccurrenceIdentity`，不得新增重复身份模型或直接修改领域状态。
- `TrayPort`、事件源和 `NotificationCenter` 的创建方拥有关闭责任；`Close` 必须释放自身资源并保持幂等，不能关闭 bootstrap 所拥有的 Store、日志或实例锁。
- `NotificationCenter.Cleanup` 是独立的可失败步骤，端口必须如实返回清理错误；正式退出时 Runtime 将其作为可观察的尽力操作，不能让它阻断资源回收。`Close` 必须等待在途提交，但不能把未完成的通知清理伪装为成功。
- 目录选择器不保存配置、不修改日志或备份资源；目录设置的持久化和重启生效策略由 Runtime/Config 正式用例负责。
- 契约保持平台无关，不得引入 Wails/Win32 依赖或通过全局时钟、全局 context 绕过运行时边界。

## wailshost

- `wailshost` 是 Wails v2.15 宿主的生命周期适配器；核心实现通过注入的 Runner、Runtime 和生命周期回调保持可测试，不在测试中打开 GUI。
- Wails 的真实构造器只负责把 v2.15 回调和 Runtime 函数接入窄 seam；`wails.Run` 必须同步且每个 Host 只能调用一次。
- `OnStartup` 只保存并转发生命周期 context；Wails Runtime 从 `OnDomReady` 回调开始可用，因此宿主在调用该回调前开放 Runtime 门闩，并在回调失败时立即进入正式停止路径。所有 Runtime 调用使用保存的 context。
- 窗口关闭偏好必须动态读取：`tray` 隐藏并拦截，`exit` 异步进入唯一 Stop 路径；`Quit` 先设置 allow-quit 门闩，再调用 Runtime Quit。
- 外部取消和关闭兜底使用 `context.WithoutCancel` 派生清理 context；Suspend/Resume 只转发给注入的环境方法，不能直接修改 Runtime 或领域状态。
