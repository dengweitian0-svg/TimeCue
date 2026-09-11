# cmd/timecue

- 本目录是 TimeCue 的唯一进程入口，当前包含 `main.go`。
- `main` 的职责是：
  - 创建响应 `os.Interrupt` 和 `SIGTERM` 的 `context`；
  - 组装 `bootstrap.Application` 实现并调用 `bootstrap.Start`；
  - 将最终错误输出到标准错误，并通过 `utils.ExitCodeFor` 转换进程退出码。
- 配置加载、配置校验、日志创建、运行时生命周期和资源关闭必须由 `internal/bootstrap` 及其依赖负责。
- 不得在 `main` 中实现业务逻辑、重复记录最终错误或绕过 `bootstrap` 直接启动应用。
- 修改入口行为时必须同步检查信号取消、正常退出、错误输出和退出码映射。
