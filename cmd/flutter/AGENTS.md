# Go 服务入口

- `main.go` 装配配置、SQLite、应用用例、真实聊天服务及 HTTP 路由。
- `managed.go` 处理启动参数、托管 ready 与父端停止控制。
- `control_windows.go` 提供 Windows 控制管道读取。
- `*_test.go` 覆盖启动参数、控制协议及真实进程的启动、退出和聊天恢复。
