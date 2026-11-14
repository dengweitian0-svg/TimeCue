# Go 服务入口

- `main.go` 接入系统和托管进程的停止信号。
- `host.go` 管理共享配置、日志、SQLite 及 App → Agent Loop 的启动顺序。Loop 必须在 Go 启动后 8 秒内被接纳，超时或失败时只提供 App 路由。
- `http.go` 管理唯一的 HTTP 服务与 ready 握手。HTTP 和 Loop 并行收尾，分别有 5 秒和 30 秒。App 收到全局退出和实际 LoopDone 后取得独立 30 秒。宿主等待服务与在途请求退出后释放共享数据库。
- `managed.go` 处理启动参数、托管 ready 编码与父端停止控制。
- `control_windows.go` 提供 Windows 控制管道读取。
- `*_test.go` 覆盖启动参数、控制协议及真实进程的启动、退出和聊天恢复。
