# SQLite 存储

- `chat.go` 使用版本 3 已有 continuation 列保存/回读逻辑续写标记，ContinuationCallCount 统计 run 内全部续写，含摘要用途，不因重试/恢复重复登记。恢复续写时读取普通请求候选，由聊天进度按实际消息前缀恢复本轮正文边界；不按主键邻接或展示事件猜测分组，不新增表、列、索引或 checkpoint 字段。

- `chat_tools.go` 沿版本 3 已有 next_retry_at/status/error 保存工具 retry_wait，重启完整回读；暂时故障不写最终结果事件或完成集合。实际重试的 retry_count、running、期限清除及 tool_use checkpoint 同事务提交，沿用原身份、参数和键，不新增 tool_start。最终结果清除退避期限，重试等待的取消与故障补齐由聊天统一进度裁决。

- `chat_tools.go`：版本 3 已有工具表的按模型调用回读、完整模型回复接纳、工具 running/开始事件登记及逐项结果事务；参数以外层 JSON 字符串保存，结果保持原 JSON 类型。开始与结果事务回读已提交 checkpoint，调用聊天完整进度操作，提交成功后返回完整快照，失败返回原快照；已终态工具复用已提交进度，不重复事件。tool_start/tool_result 与关联、消息、completed_tool/last_tool 和 checkpoint 原子保存，取消跳过不伪造 running；结果事务失败时可在运行失败收尾事务中复用同一逐项合并保存实际结果。无表、列、索引或迁移变化。

提供日程与提醒的持久化、事务操作和数据库健康检查，满足 `service/schedule` 与 `service/notification` 定义的存储接口。健康检查验证通知 schedule_id/notify_time 完整唯一键；Unix 纳秒时间写入前拒绝超出 int64 范围的日程及提醒时间。

- `db.go`：SQLite 连接与关闭、版本 3 表结构和 loaded 状态约束检查（当前 001 + 002 + 003）。
- `schedule.go`：日程创建、查询、暂停/恢复、取消、软删除，日程与首提醒联合创建，以及日期/待确认事务快照读取。
- `notification.go`：提醒生成与去重、pending/loaded 时间区间查询、详情/历史查询、投递结果更新、软删除及单条/批量确认。
- `notification_loading.go`：取得写锁后读取完整规则版本并纯计算候选，幂等预加载和到期快照、停止版本校验、完整分钟事务。
- `notification_delivery.go`：同一事务重检到期/快照/停止/确认及父规则资格，按 D2 许可重加载 failed；原子回填冻结成员，保护 sent 并保留确认和停止事实。
- `notification_recovery.go`：写锁内按版本继续位置补齐至多 500 条历史事实，复用唯一键、冻结可靠历史文本并核验触发后的停止版本；提交后返回继续位置。
- `notification_members.go`：最早恢复边界、本轮历史未确认数量、当前可展示成员数量及任务成员事务分页；未确认统计只看本轮已到期、未删除且未确认的事实，不按投递状态或父规则资格过滤；使用 json_each 的单参数集合，避免 SQLite 变量上限。
- `notification_recovery_test.go`：长历史、恢复中删除、暂停/恢复快照、预算保留、事务回滚、重启、分页、停止竞争、跨轮次去重及恢复期间实时投递测试。
- `notification_delivery_test.go`：大组不拆分、原子回滚、结果重传、并发开始与确认、当前分钟失败重启及原四张表结构保护测试。
- `notification_loading_test.go`：近期即时入队、整分钟大组、版本快照、规则/停止竞争、失败、重启、回拨、回滚和表约束测试。
- `store_test.go`：数据库事务、状态更新、历史保留与业务查询集成测试。
- `db_schema_test.go`：健康检查拒绝缺失通知唯一键的结构，并接受完整非 partial 唯一索引。

# 约束

- `Connect` 只打开连接；启动显式调用 `PrepareSchema` 后健康检查验证迁移版本和结构。核心业务持久化使用 `schedules`/`notifications`；`schedule_versions` 保存规则有效区间，`schedule_write_receipts` 保存请求摘要与原操作结果。
- `migrate.go` / `migrate_test.go`：嵌入现有 001/002/003，空库初始化、Flutter 版本 1/旧版本 2 升级、SQLite 一致性备份、保留提醒主键/投递/确认事实及索引；升级备份前检查通知 schedule_id/notify_time 唯一键，事务提交前检查结构，失败回滚。数据库版本为 3，原四张日程业务表保留；Wails 和未知列集合拒绝，不覆盖用户数据库。
- 日程与首提醒联合创建、日程取消/删除及未来待投递提醒隐藏使用事务；提醒生成在事务内检查父日程状态并按日程/触发时间去重。
- 使用软删除保留历史；日期与待确认组合查询使用同一读取事务。投递结果与确认分别更新，批量确认任一非法成员整批回滚，重复确认保留首次时间。
- 数据库时间保存 Unix 纳秒，返回时间固定 UTC+8；公开错误保留 `%w`，便于上层使用 `errors.Is`。
- 投递协议不新增表、字段或迁移，保留原四张业务表，AI 独立迁移后数据库版本为 3。成员资格及整组结果使用单个事务；成员数不沿用确认接口的 500 条上限。开始后停止不阻止真实平台结果落库，回填不自动确认。
- 全期间恢复同样不改变结构；500 条限制只作用于历史补齐事务，不拆完整 start/results。恢复不覆写已有快照、投递结果、确认及停止，不自行重加载 failed；后续停止版本使尚未补造的旧事实永久停止，即使父规则已经恢复。
- 成员总数、页数据和父日程在同一读取事务取得；包含历史已确认及停止成员，保留已删除父日程内容。可展示统计为只读操作，不修改状态或预算。

- `schedule_change.go`：获取事务写锁后检查版本；原子保存生效区间、到期文本、未来提醒及幂等回执。暂停/删除设置 delivery_stopped，恢复只复用未触发、可撤销的未来 pending/loaded，不清除旧到期记录的停止标记。
- 创建、更新和删除只在事务提交成功后返回变更结果；更新/删除结果包含事务内旧状态，删除结果还包含实际写入后的状态。幂等命中显式返回 `Replayed=true`，不返回本次变更前态，不改写已有回执格式或数据库结构。
- `schedule_change_test.go`：13:00 → 17:00、历史名称及失败结果、暂停/恢复与重启、删除后确认、并发版本、事务回滚、已到期单次与未来唯一键复用。
- `schedule_change.go` 的 ApplyScheduleDelete 与传统停止操作共用事务实现，在写锁内优先核对原回执，原子完成版本检查、软删除、到期快照、未来提醒撤销、投递停止、版本区间及业务回执；ReadScheduleReceipt 为独立只读 SELECT，未命中返回 nil。schedule_delete_test.go 通过正式 App 和真实 SQLite 验证版本竞争、回执失败回滚、重复调用、提醒和只读核对。
- `chat_tools.go` 从版本 3 已有 idempotency_key 列保存并回读后台生成的危险工具身份；原参数、running、键、事件及 checkpoint 同事务登记。恢复保留原键，非危险工具仍保存 SQL NULL，结果保存不改变执行身份；不增加数据库结构。

- `open_schedule_count_test.go`：用实际 SQLite 读取验证日期的完整启用日程数，覆盖超过 50 条分页、同日多次去重、已确认单次/周期、暂停/删除/取消及空日期。

ReadUnacknowledged 在同一事务读取范围完整总数、有界提醒页及对应父日程。readUnacknowledged 与 ReadScheduleState 共用未确认、未删除、到期及排序条件，位置只影响页数据；不以父日程或投递状态过滤。unacknowledged_scope_test.go 使用真实 SQLite 验证零点、分页、状态保留、确认及游标日期隔离。

- `chat.go`：六张 AI 表的结构健康检查与原子会话/run 创建、幂等回读、逻辑模型调用及 checkpoint 登记、部分输出/事件提交、正常退出进度保存、终态与 usage 提交、软删除可见性、历史上下文、事件游标分页及未完成运行扫描。AI 表使用业务 run/session ID 外键，无级联删除，不增加查询索引。
- 聊天批次逐条保留原始事件与 data，在同一事务保存部分响应并分配连续 seq；失败整批回滚。正常退出的尾批与调用快照原子保存，终态尾批与调用结果、usage、checkpoint、运行状态及 error/end 原子保存；结构保持版本 3。

- 有效模型上下文只纳入已登记模型调用的历史；预算拒绝等未进入模型调用的输入保留在原始会话历史和事件中，不参与后续模型预算。
- History 按 run.created_at 选择最新终态 checkpoint 后，调用聊天进度规则接续完整 context，不按 model_call.id 重建。回复接纳、调用开始/结束快照与旧格式兼容规则统一由 chat/progress.go 生成；存储只序列化三字段状态并保留事务核验、原子提交和事件编号。会话 run_ids 同样按创建时间升序，时间相同以 run 主键稳定排序。
- 无可压缩历史的预算拒绝在 Create 事务保存失败 run 与 start/error/end，不创建模型调用、checkpoint 或 compact。OrdinaryCallCount 从已有 continuation 与 compact 关联统计普通逻辑调用；ReadCall 同时回读 checkpoint 阶段与完整消息，不新增表、列或运行计数字段。
- 聊天调用复用版本 3 已有 retry_count/max_retry/next_retry_at/retry_history 列；调用快照、重试事实及适用尾批同事务提交，ReadCall 完整回读以支持退避恢复，不新增表、列、索引或迁移。正常退出保留 retry_wait 的原错误和期限，终态清除计划期限。

聊天异常收尾同一事务复用已提交工具结果，并将已接纳但尚无确认结果的原 ID 补齐 tool_result_unconfirmed；取消未启动项补齐 skipped/aborted。开始与结果事件和完整工具配对均保留，直接保存最终处理事实，不伪造 running/tool_use；正常退出不补造终态，保留原进度供恢复。

待保存工具结果仅携带实际取得的事实。Finish 在同一事务回读最新调用、checkpoint 与全部工具记录，调用聊天统一收尾操作后复用正常结果的事实及事件保存；存储不自行生成跳过或未确认结果。已终态调用不重写 output/usage/结束时间，未变化的已完成 checkpoint 保持原值；结果回执丢失时优先复用已提交事实，终态失败整体回滚，重试不重复结果或 end。不新增存储格式或迁移。

- RequestCancel 原子将 running 改为 wait_for_cancel，不覆盖终态。Hide 在同一事务软删除父会话并保存其活动运行取消意图；所有子记录保留。Finish 重检已提交取消意图，取消与正常完成竞争时保存 cancelled 和唯一 end，不采用旧的成功快照。

- `chat.go` 在版本 3 上为首次摘要原子保存 model_call、compact_succeeded=0 的 compact 和 compact checkpoint，重试/恢复/续写不新增 compact。CompleteCompact 保存已由聊天进度生成的成功投影、原调用结果和尾批、compact_succeeded=1、session.compact_event_id；预算允许时同事务登记下一普通调用和 checkpoint，否则保留 compact 的有效投影。事务内重检取消意图，失败不切换有效分界；History 将最新快照和未生效摘要运行的原输入交统一进度接续，不纳入部分摘要。无表、列、索引或迁移变更。

- 工具结果事务独立保存完整原始 output/事件与统一进度生成的有界模型文本，后续读取只复用已提交 context。运行中摘要的 compact checkpoint 在成功前保留完整普通上下文，History 直接接续该有效快照；readCall 复用普通请求前缀核验恢复先前普通正文，摘要专用 input/output 不参与用户正文投影。成功切换复用既有原子事务，无结构变更。
- CompleteCompact 在下一调用为空时，与成功摘要和 checkpoint 同事务保存协调器给出的预算失败运行及 error/end；普通终态和摘要终态共用 finishChatRun 写入。取消先提交仍不启用摘要，事务失败整体回滚，无成功摘要与 running 终态之间的恢复窗口。
