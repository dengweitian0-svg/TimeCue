# internal/platform/windows

- 本目录负责 Windows 单实例守卫和跨进程激活适配，不进入 Wails、SQLite 或运行时业务流程。
- `WindowsInstanceBroker` 同时实现 `bootstrap.ExclusiveStartup` 与 `platform.ActivationSource`。主实例必须在 `Acquire` 返回成功前创建命名 Pipe listener；释放函数由 bootstrap 持有并负责最终关闭。
- Mutex 与 Pipe 名称必须包含当前登录用户 SID。Pipe 使用显式 SDDL，只允许当前用户和 SYSTEM；不得依赖默认 ACL，也不得记录原始命令行或 Pipe payload。
- Pipe 消息有固定大小上限，协议严格校验 kind、正整数 `schedule_id` 和 `YYYY-MM-DD` `original_date`。Toast 无主实例时返回明确的 `ErrActivationWithoutInstance`，调用方必须在进入数据库/UI 前结束进程。
- Windows 实现通过 build tag 隔离；非 Windows stub 只用于保持跨平台编译并明确返回不支持错误。纯协议解析必须放在无平台依赖文件中并配套测试。
