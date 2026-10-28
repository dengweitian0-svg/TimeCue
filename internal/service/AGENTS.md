# 领域服务模块

- `schedule/`：日程规则校验、正式写入、版本幂等删除、只读回执核对、日历与事实/投影查询。
- `notification/`：提醒生成、调度、确认、投递状态、attempt、成员和展示预算。
- `recovery/`：恢复轮次、遗漏窗口、历史批次及恢复进度。

# 约束

- 服务依赖窄存储端口与共享 model，不依赖 App、API 或具体 store。
- Schedule 导出纯规则计算；恢复通过值类型和明确操作与投递服务交接。
- 投递运行状态由 notification 持有，恢复游标与去重状态由 recovery 持有。
