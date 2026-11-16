# 应用编排

- `app.go` 装配日程与提醒领域服务，提供应用用例入口。
- `schedule.go`、`notification.go`、`recovery.go` 和 `query.go` 编排写入、恢复和组合查询。
- `runtime.go` 启动提醒调度和恢复，发布 AppReady，并等待全局退出与实际 LoopDone 后收尾。
- [提醒恢复测试](notification_recovery_test.go)覆盖恢复任务的取消、恢复窗口内触发点的归属，以及恢复准备对当前分钟提醒组的保留。
- Go 宿主的启动顺序、Agent Loop 启动失败或超时时的降级，以及协调退出，见[宿主生命周期测试](../../cmd/flutter/lifecycle_test.go)。

## 工程约束

- 跨服务正式写入由 App 统一串行协调，带期限的调用可取消排队。平台投递结果通过正式回填用例提交。
