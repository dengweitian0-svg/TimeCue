# 日程服务

- `schedule.go` 提供日程事实、投影与分页查询，`rules.go` 处理周期规则及触发计算。
- `schedule_change.go` 和 `schedule_delete.go` 处理正式写入、版本删除及回执。
- `schedule_mutation.go` 生成变更摘要，`*_test.go` 覆盖规则和变更。

## 工程约束

- 规则变更及联动提醒通过正式存储事务提交。
