# 应用编排

- `app.go` 装配领域服务与聊天端口，提供应用用例入口。
- `schedule.go`、`notification.go`、`recovery.go` 和 `query.go` 编排写入、恢复和组合查询。
- `runtime.go` 和 `runtime_info.go` 管理本机 HTTP 服务、运行身份和宿主生命周期。
- `*_test.go` 覆盖应用装配、生命周期和提醒恢复。

## 工程约束

- 跨服务正式写入由 App 统一串行协调。平台投递结果通过正式回填用例提交。
- 聊天收尾和 HTTP 停止共用宿主退出期限。
