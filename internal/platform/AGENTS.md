# internal/platform

- 本目录只定义桌面边界的跨平台窄契约和测试 fake，不实现 Wails、Win32、Toast、托盘、配置或 Runtime 组装。
- `DesktopHost` 负责主循环和退出请求；`WindowController` 只负责窗口动作；`FrontendEvents` 只发送受控边界事件。
- `ActivationSource` 和 `EnvironmentEventSource` 只能把外部事实作为事件输出。激活目标复用 `schedule/domain.OccurrenceIdentity`，不得新增重复身份模型或直接修改领域状态。
- `TrayPort`、事件源和 `NotificationCenter` 的创建方拥有关闭责任；`Close` 必须释放自身资源并保持幂等，不能关闭 bootstrap 所拥有的 Store、日志或实例锁。
- `NotificationCenter.Cleanup` 是独立的可失败、可重试步骤；`Close` 必须等待在途提交，但不能把未完成的通知清理伪装为成功。
- 契约保持平台无关，不得引入 Wails/Win32 依赖或通过全局时钟、全局 context 绕过运行时边界。
