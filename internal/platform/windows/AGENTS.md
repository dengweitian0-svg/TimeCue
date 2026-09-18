# internal/platform/windows

- 本目录负责 Windows 单实例守卫和跨进程激活适配，不进入 Wails、SQLite 或运行时业务流程。
- `WindowsInstanceBroker` 同时实现 `bootstrap.ExclusiveStartup` 与 `platform.ActivationSource`。主实例必须在 `Acquire` 返回成功前创建命名 Pipe listener；释放函数由 bootstrap 持有并负责最终关闭。
- Mutex 与 Pipe 名称必须包含当前登录用户 SID。Pipe 使用显式 SDDL，只允许当前用户和 SYSTEM；不得依赖默认 ACL，也不得记录原始命令行或 Pipe payload。
- Pipe 消息有固定大小上限，协议严格校验 kind、正整数 `schedule_id` 和 `YYYY-MM-DD` `original_date`。Toast 无主实例时返回明确的 `ErrActivationWithoutInstance`，调用方必须在进入数据库/UI 前结束进程。
- Windows 实现通过 build tag 隔离；非 Windows stub 只用于保持跨平台编译并明确返回不支持错误。纯协议解析必须放在无平台依赖文件中并配套测试。
- `EnvironmentClock` 同时实现 `app.Clock` 与环境事件源，`Snapshot` 每次读取都采样当前墙钟，并与事件使用同一份时区位置；普通快照不得携带仅属于事件的变化标志。
- 环境变化必须先刷新共享快照再发出 `app.RuntimeEvent`；关闭后通过 epoch/关闭状态拒绝旧回调，事件队列和处理错误不得静默丢弃。
- 墙钟、时区和后续托盘消息共用一个受控 `MessageThread`。消息泵必须锁定 OS 线程，处理器通过令牌注册/移除；由于 `WM_SETTINGCHANGE` 的 `lParam` 不是可靠的时区标识，收到该低频消息时统一刷新动态时区规则。
- 时区读取使用 Windows 动态规则生成带 64 位转换区块的 TZif，保留可表示的 v1 区块和规范结尾，不能依赖缓存的 `time.Local` 或当前固定偏移。
- 通知发送与点击回调只包装 Wails Notification Runtime；`Data` 只包含稳定的 `schedule_id` 与 `original_date`，系统历史清理由独立的应用级 WinRT 清理器承担。`0x80070490` 表示无当前应用通知可清理，必须视为幂等成功；其他清理错误必须如实返回 Runtime，由 Runtime 记录告警并继续正式退出。
- 托盘只暴露“打开 TimeCue”和“退出”两个动作，并与环境监听共用同一个 `MessageThread`；关闭时先拒绝新回调、删除图标并等待已进入的回调，再由资源所有者关闭消息线程。同一次右键操作可能同时送达 `WM_RBUTTONUP` 与 `WM_CONTEXTMENU`，嵌套菜单期间必须去重，避免重复弹出菜单阻断正式退出。
