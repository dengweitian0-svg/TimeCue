# Go 服务入口

启动供 Flutter 调用的本机 HTTP 服务。

- `main.go`：创建配置/SQLite/仓储适配器，装配业务 App 与 HTTP 路由，通过退出信号 context 调用 service.App.Run。
- 监听 `127.0.0.1:10086`，处理退出信号、HTTP 优雅关闭及数据库和日志文件清理。

工程约束：入口只保留关键装配和信号处理；配置加载、监听前数据库检查、HTTP/调度运行及退出清理由 service.App 管理。数据库结构不在启动流程自动修改。
