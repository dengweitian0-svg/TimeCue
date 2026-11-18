# 提醒服务

- `notification.go` 提供提醒查询、确认和存储端口。
- `next_notify.go` 生成下一条提醒，`notification_loading.go` 和 `scheduler.go` 管理预加载、分钟核验和调度。
- `notification_delivery.go` 管理投递任务、尝试及回填，`notification_members.go` 提供成员查询。
- `*_test.go` 覆盖调度与投递。

## 工程约束

- 投递尝试与展示预算由本模块管理，用户确认独立于投递结果。平台结果未知时保留未知。
