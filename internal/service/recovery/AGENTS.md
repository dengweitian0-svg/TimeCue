# 恢复服务

- `notification_recovery.go` 管理恢复轮次、遗漏窗口、历史批次和只读进度。
- `recovery_candidates.go` 展开日程历史版本，`recovery_candidates_test.go` 验证版本及批次边界。

## 工程约束

- 恢复只依据可证明的版本和事实，通过正式操作与投递服务交接。
