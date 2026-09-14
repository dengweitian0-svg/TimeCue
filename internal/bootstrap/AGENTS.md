# internal/bootstrap

- 本目录负责应用启动、运行、退出和资源清理的顶层编排。
- 当前文件：
- `bootstrap.go`：定义独占启动资格、受控 `Store` 和 `Resources`，实现 `Start`、`Initialize` 和 `Run`。
  - `bootstrap_test.go`、`initialize_test.go`：验证启动顺序、资格拒绝、部分失败清理、关闭错误、取消退出和日志结果。
- `Application.Run` 接收 `context.Context`、已校验的 `config.Config`、`*slog.Logger` 和 bootstrap 注入的 `Store`，返回应用运行错误。
- `Start` 负责加载配置；配置加载失败时包装为 `ConfigError` 并直接返回，不进入初始化。`Initialize` 严格按配置校验、独占资格、目录准备、日志和 SQLite 的顺序创建资源；未获资格时不得创建目录、打开或迁移数据库。
- bootstrap 持有 `Store`、日志文件和独占资格释放函数；失败和退出按 `Store → 日志文件 → 独占资格` 逆序关闭，并通过 `errors.Join` 保留所有 cause。
- 只有值恰好等于 `context.Canceled` 才表示正常主动退出；包装错误或组合错误必须保留、包装为运行错误，并在此顶层边界记录一次最终 `ERROR`。
- 日志文件由 `Initialize` 创建、由 `Resources` 持有并关闭；关闭失败不得被静默忽略，且应与已有运行错误合并。
- 生命周期日志只记录已有明确边界的事件：启动和停止使用 `INFO`，运行失败使用 `ERROR`；当前 `Application` 没有独立就绪契约，不记录 `application ready`。
- 本目录只编排生命周期，不实现具体 UI、存储、调度或领域业务；SQLite 创建通过可替换 opener 接入，运行单元通过窄 `Store` 接口接收资源。
- 修改启动顺序、错误分类或资源清理时必须同步更新本目录测试，并检查正常退出与失败退出两条路径。
