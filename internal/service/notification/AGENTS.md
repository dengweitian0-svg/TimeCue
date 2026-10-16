# 提醒服务

- `notification.go`：提醒查询、详情、确认和存储端口。
- `next_notify.go`：按当前日程规则幂等生成下一条提醒。
- `notification_loading.go`、`scheduler.go`：规则版本预加载、分钟核验和内存分组。
- `notification_delivery.go`：投递任务、attempt、成员快照、展示预算与结果回填。
- `notification_members.go`：任务和 attempt 成员分页。
- `scheduler_test.go`、`notification_delivery_test.go`：调度与投递协议测试。

# 约束

- 投递任务、attempt、成员归属和展示预算由本模块持有，运行状态只在当前进程内。
- 有效开始才消费展示次数；回填使用冻结成员，可信迟到 sent 结果可以回流。
- 调度内存锁独立于 App.operations；本模块不依赖 App、API 或具体存储实现。
- loaded 只表示加载/等待执行，sent 表示平台接受展示；确认保持独立，重复确认保留首次时间。确认集合最多 500 条，整批校验后原子提交。
- 每次最多领取 20 个完整任务；重复 claim 复用未开始身份，重复 start 返回冻结决定。有效开始最多四次，重试间隔为 5/30/120 秒，重扫不重置预算。
- 任务和 attempt 成员每页 50 条，游标绑定用途、任务、attempt 与成员版本；成员变化拒绝旧续页，旧运行身份失效。
- 明确执行者退出只整理指定活动 attempt；未知平台结果不伪造失败，不使用超时、租约或心跳自动重新展示；成功结果不降级。
- loaded 先提交再入内存队列，完整分钟核验完成才开放领取；Snapshot 返回副本，不持调度内存锁访问数据库。
- 待确认 scope=today/history 依据原计划时间和固定 UTC+8 零点划分，游标绑定范围、北京日期和首分页截止时刻；详情与查询不确认、不推进恢复。
