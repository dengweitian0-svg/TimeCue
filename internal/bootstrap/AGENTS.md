# internal/bootstrap

- 本目录负责应用启动、运行、退出和资源清理的顶层编排。
- 当前文件：
  - `bootstrap.go`：定义 `Application` 接口，实现 `Start` 和 `Run`。
  - `bootstrap_test.go`：验证启动顺序、错误处理、取消退出和日志结果。
- `Application.Run` 接收 `context.Context`、已校验的 `config.Config` 和 `*slog.Logger`，返回应用运行错误。
- `Start` 负责加载配置；配置加载失败时包装为 `ConfigError` 并直接返回，不进入 `Run`，加载成功后才把配置交给 `Run`。`Run` 负责再次校验配置、日志初始化、应用运行、顶层错误包装和日志文件关闭。
- `context.Canceled` 表示正常主动退出；应用运行失败必须包装为运行错误，并在此顶层边界记录一次最终 `ERROR`。
- 日志文件由 `Run` 创建时取得，必须由 `Run` 负责关闭；关闭失败不得被静默忽略，且应与已有运行错误合并。
- 本目录只编排生命周期，不实现具体 UI、存储、调度或领域业务；新增应用组件应通过 `Application` 或窄接口接入。
- 修改启动顺序、错误分类或资源清理时必须同步更新本目录测试，并检查正常退出与失败退出两条路径。
