# 日程与提醒请求处理

- 日程 Handler 处理创建、更新、查询和删除，提醒 Handler 处理查询与确认。
- 投递 Handler 处理领取、开始、结果回填和执行者退出。
- `error.go`、`schedule_log.go` 与 `delivery_http_helpers.go` 提供错误、日志和投递响应适配。

## 工程约束

- 通过 `internal/app` 的正式用例读写业务状态。
