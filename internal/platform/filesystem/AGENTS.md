# internal/platform/filesystem

- 本目录只提供本地文件系统对 `platform.DirectoryAccess` 的具体适配。
- 目录校验统一复用 `internal/config.ValidateDirectoryPath`，包括缺失目录创建、物理路径规范化以及临时文件写入/同步/删除探测；不得在这里复制配置持久化或运行时切换逻辑。
- 该适配不拥有日志、SQLite Store 或应用生命周期资源；运行时只在保存目录设置的应用用例中调用它。
