# internal/config

- 本模块负责使用 Viper 加载默认配置和用户配置，并提供配置热更新。
- `default.yaml` 通过 `go:embed` 作为只读默认基线；任何加载、监听或未来的配置写入都不得修改它。
- 用户配置文件固定为当前工作目录下的 `config.yaml`，文件不存在时只使用默认值；文件存在后，用户值只能覆盖默认值。
- 配置快照通过 `atomic.Pointer[Config]` 使用 `Load`/`Store` 原子读取和替换，禁止原地修改已发布快照。
- `LoadConfig` 首次成功加载后自动启动 Viper 文件监听；热更新失败时必须保留旧配置。
- `CurrentConfig` 返回值快照，调用方不得依赖或修改内部配置指针。
- `ChangeConfig`、`ChangeLog` 和 `ChangeDB` 只修改 `config.yaml` 的用户配置层，禁止修改 `app`；缺失文件或 key 时创建/追加，已有 key 时覆盖。
- `RestoreDefault` 通过原子替换将 `config.yaml` 清空，恢复默认值；默认配置文件本身不可写。
- 用户配置写入必须通过同目录临时文件、写入、同步和替换完成，避免产生半写入文件。
