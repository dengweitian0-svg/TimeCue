# 界面数据契约

- `notebook_repository.dart`：定义日程类型、编辑草稿、日程/触发摘要，以及查询、分页、确认和规则编辑所用的仓储接口。草稿包含逐项的 ScheduleTimeSlot（周期类型、小时、分钟、单个星期/日号）、兼容旧草稿的时间/日期集合及不可映射的原始 cron；日程摘要携带 revision。

- `schedule_rules.dart`：受限五字段 cron 与独立时间项的无损转换，按同一时间合并其真实日期集合，不生成跨项组合；不计算下一次触发，复杂旧规则按原样保留。
- `notification_delivery.dart`：NotificationDeliveryGateway 定义 health、领取、开始和结果回填；DeliveryRuntime/Claim/Start 校验 Go 运行能力、任务身份和展示决定，DeliveryOutcome 仅含 sent/failed。该窄契约独立于 NotebookRepository，演示仓储不承担真实平台投递，不定义成员、预算或跨重启回执。
- `notification_query.dart`：NotificationQueryGateway 仅定义运行身份、持久提醒详情及任务/attempt 成员分页；NotificationTarget 校验四字段激活 payload，NotificationMemberPage 携带真实总数、不透明游标和可选恢复摘要。查询不包含业务写入或平台操作。
- `notebook_repository.dart` 的 RecoveryProgress 保存组合快照恢复摘要，区分 running/completed/failed、已整理/遗漏/可投递数量和未知总数；NotebookRepository 默认无恢复，示例仓储不承担真实激活。

`openScheduleCountOn` 返回该日期完整的启用日程总数；未知快照返回 null。确认状态不影响数量，同一规则多次触发只计一条日程；HTTP 实现读取 Go 的聚合值。

PendingScope 表达今日和历史两个读取范围，pendingFor/pendingTotalFor/pendingPageIndexFor/pendingLoadingFor/pendingErrorFor 分别返回页面状态；pendingTotal 保留房间完整待确认数。pendingCurrent 表示范围快照仍属于当前北京日期；确认始终提交明确 ID。
