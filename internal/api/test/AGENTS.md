# API 测试

- 本目录包含 API 路由、HTTP、SSE、日志、恢复和投递协议测试，使用外部测试包 `api_test`。
- 通过 `api.NewRouter` 验证公开 HTTP 行为，包括 session 名称、空 run 列表和错误映射；共享 HTTP 测试辅助函数保留在对应测试文件中。
- SQL 迁移文件从本目录以 `../../../migrations` 相对路径读取。
- 日志捕获测试需串行运行，避免全局日志配置相互影响。
