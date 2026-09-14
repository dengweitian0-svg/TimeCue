# internal/config

- 本目录负责配置来源、默认值、YAML 解析、结构化反序列化和启动阶段校验。
- 当前文件：
  - `config.go`：定义 `Config`、`AppConfig`、`LogConfig`、`BackupConfig` 和 `ResolvedPaths`，实现配置加载、默认值注入、校验和固定路径解析。
  - `defaults.yaml`：作为无工作目录配置时的内嵌 YAML 默认配置来源。
  - `config_test.go`：验证默认配置、工作目录配置、非法日志设置和路径解析。
- `Load` 先通过 `setDefaults` 为配置字段设置稳定默认值，再优先读取 `timecue.exe` 所在目录的 `config.yaml`；文件不存在时读取内嵌的 `defaults.yaml`。两条路径都必须完成反序列化、校验和路径解析。
- 当前日志配置约束：级别只能为 `debug`、`info`、`warn` 或 `error`；路径不能为空且必须使用 `.log` 扩展名。
- 备份目录不能为空；相对日志和备份路径必须基于可执行文件目录解析为规范绝对路径，绝对路径必须保留其绝对语义。
- `ResolvedPaths.DatabasePath` 固定为 `%LOCALAPPDATA%\\TimeCue\\timecue.db`，不能被配置覆盖；旧 `data_dir` 以及数据库路径相关配置必须显式拒绝。
- `ResolvedPaths` 是后续启动编排和存储初始化使用的受控路径视图，配置包只负责来源、解析和校验，不创建目录或执行应用生命周期。
- 本目录不得初始化日志器、执行应用生命周期或承载业务逻辑；配置错误应返回并保留有效上下文。
- 新增配置字段时必须同步考虑默认值、解析映射、校验、路径语义和对应测试。
