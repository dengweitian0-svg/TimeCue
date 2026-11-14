# 应用编排

- `app.go` 装配日程与提醒领域服务，提供应用用例入口。
- `schedule.go`、`notification.go`、`recovery.go` 和 `query.go` 编排写入、恢复和组合查询。
- `runtime.go` 启动提醒调度和恢复，发布 AppReady，并等待全局退出与实际 LoopDone 后收尾。
- `*_test.go` 覆盖应用装配、生命周期和提醒恢复。

## 工程约束

- 跨服务正式写入由 App 统一串行协调，带期限的调用可取消排队。平台投递结果通过正式回填用例提交。
