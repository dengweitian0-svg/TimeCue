# Go 服务入口

启动供 Flutter 调用的本机 HTTP 服务。

- `main.go`：校验启动参数，创建配置/SQLite/仓储适配器与不可变运行信息，装配 `internal/app.App`（含聊天服务端口）与 HTTP 路由，将信号和父端控制合入原 context 调用 `App.Run`。
- `managed.go`：`--data-dir` / `--instance-id` / `--port` 校验、可执行文件目录默认值、stdout 单行 ready 编码、有界 stdin shutdown/EOF 控制及读取工作收尾。
- `control_windows.go`：Windows 可关闭控制 Reader，只在专用匿名管道已有字节时读取，避免同步读取阻塞退出。
- `managed_test.go` / `process_test.go`：参数和控制协议、真实匿名管道收尾、编译后二进制握手/健康/退出、启动失败及重启旧身份失效测试。
- `process_chat_test.go`：复用编译后二进制及隔离目录，验证普通/摘要续写正常退出的部分 output、thinking、usage 和尾批、原调用/请求/预算恢复、阻塞恢复期间 ready/health 可用，以及无 Key 时取消与隐藏会话的只读收尾和终态不重执。
- 默认监听 `127.0.0.1:10086`，可用 `--port` 指定 1–65535 的 loopback 端口；ready 地址与路由 Host 校验使用同一端口。处理退出信号、HTTP 优雅关闭及数据库和日志文件清理。

# 约束

入口面向 Windows，只保留关键装配和信号处理；先创建数据目录，向 App.Run 注入存储 PrepareSchema，在健康检查和监听前初始化空库或升级可识别的 Flutter 旧库。退出等待恢复批次与已接收请求后再关闭资源；Wails 和未知数据库拒绝自动转换。

聊天用例由入口分别注入 App.ChatServices 的 ModelService、RunService、SessionService；App.ChatRuntime 独立管理生命周期，使用真实模型与 SQLite。

手动启动省略 data-dir 时使用 Go 可执行文件所在目录，配置位于该目录的 config.yaml；相对数据库和日志路径使用同一基准。显式 data-dir 可覆盖默认位置，相对参数按启动工作目录解析；开发使用 `go run ./cmd/flutter --data-dir . --port 1145`。托管入口仍要求宿主显式传入共同数据目录，不通过 Chdir 推测配置位置。

显式 instance-id 才进入托管模式，必须同时指定 data-dir；身份只允许 1–128 个 ASCII 字母、数字、连字符或下划线。托管 stdout 只输出一次 ready，Gin 使用 release 模式；手动运行不读取 stdin 控制，每次生成新身份。停止、EOF 和信号均只取消原运行 context，协议及启动错误返回非零退出码。管道读取必须在退出时关闭并等待；Windows 适配不提供命名管道服务、父 PID 轮询或心跳。固定端口冲突明确失败，不结束其他进程。固定端口的生命周期与进程测试用 `go test -p 1` 串行运行。

- `main.go` 装配运行、会话、模型执行、工具事务与事件查询五个 SQLite 持久化端口、真实 chat.Runtime、Chat Completions 客户端与 Manager.ChatModels；正常、managed 与 external 入口共用，无聊天 Mock 或播种。
- `main.go` 通过统一 Tool Registry 注册正式日程单条、列表和日期查询，以及串行创建/修改工具；写入复用 App.WriteSchedule、空业务 key 和版本保护。注入 chat.Runtime 及工具事务持久化端口；工具注册失败时不启动服务。
- `main.go` 同一 Registry 注册串行 delete_schedule，作为唯一危险工具，使用 App.DeleteScheduleVersion 的版本和业务回执事务；独立只读回执端口用于核对，不增加 HTTP 接口。
