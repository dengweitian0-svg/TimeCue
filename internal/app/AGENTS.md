# 应用编排模块

- `app.go`：组装日程、提醒与恢复服务，保留 App 用例门面和正式操作锁。
- `schedule.go`、`notification.go`、`recovery.go`、`query.go`：编排领域服务、统一写入边界和只读组合查询。
- `runtime.go`、`runtime_info.go`：本机 HTTP 服务、配置/数据库生命周期、监听端口和运行身份。

# 约束

- App 管理跨服务写入顺序与生命周期；Service 不依赖 App、API 或具体存储实现。
- 日程、确认、投递 claim/start/results 共用 App.operations；Scheduler 内存锁单独管理。
- 已接收投递结果在退出清理窗口内仍可回填；组合查询读取日程和提醒时使用同一存储快照。
- 生命周期在数据库 Connect 后、健康检查和监听前执行可选 PrepareDatabase；失败不发布 ready。运行身份由入口构造，健康与 ready 使用同一身份，ready 不等待历史恢复。
- HTTP 服务只监听 loopback；RuntimeOptions.Port 为 0 时使用默认端口 10086。
- 退出先停止新领取和开始、取消后台工作；HTTP 有 5 秒优雅窗口，超时关闭连接后仍等待活跃业务及后台结束，再关闭数据库、配置监听与日志。
- 入口注入具体存储、HTTP Handler 和聊天端口；App 不导入 API 或具体 store。平台投递结果须通过正式回填操作进入业务状态。

- 聊天生命周期在数据库迁移及健康检查后，先取得本机监听端口，再调用 Start 重建占用；HTTP 在恢复登记后才开始服务；退出先 Stop 取消模型请求并保存实际部分进度，再排空 HTTP，聊天完成后才关闭 SQLite。

- 用例按 ModelService、RunService、SessionService 分别注入；HTTP Handler 只使用相应窄接口，App 通过独立 Lifecycle 管理启动与停止。

- 端口冲突不进入聊天恢复；取得监听端口后的任意启动失败均释放端口，包含聊天启动失败和取消。app_test.go 在启动故障边界验证端口准入和释放。
