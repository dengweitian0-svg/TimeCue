# TimeCue 生产前端

这是 Wails 桌面应用使用的 React + TypeScript 前端。生产页面接入日程创建、列表/游标分页、删除、关闭偏好、日志/备份目录设置、运行状态清理重试、待处理提醒、逐条确认和激活握手；原型控制台和主题持久化不属于此入口。

`src/wails-adapter.ts` 的 `WailsAdapter` 只依赖可注入的 `BindingsPort`。生产入口通过静态导入接入 `wailsjs/go/runtime/WailsBindings` 和 `wailsjs/runtime/runtime`，并用显式包装把生成 DTO 收敛到页面端口；单元测试仍通过端口注入完成，不伪造生成模块。

创建和删除 mutation 使用 `JSON.stringify` payload，以保留后端 JSON 字段存在性；查询、偏好、提醒确认和 `FrontendReady` 使用 snake_case DTO。提醒查询绑定为 `ListPendingReminders`、`GetReminderDetail(schedule_id, original_date)` 和 `ConfirmReminder(schedule_id, original_date)`；前端内部把 Go `int64` 稳定身份呈现为 string，超出 JavaScript 安全整数范围的调用会明确失败。提醒正文由当前日程事实重建，`title`、`notes`、时间和处理时间均按可空字段转换，内容不可用时不会用空字符串伪造正文。

## 通知页面状态与边界

通知平台和页面之间有四个可观察边界，不能互相替代：

1. **通知端口接收**：#14 的 `ActivationSource` 接收系统激活事实，runtime 为事件分配单调 `sequence`，前端通过 `FrontendReady(last_sequence)` 完成就绪握手。
2. **系统实际显示**：Windows Toast 是否成功提交、是否出现在通知中心，必须由平台适配器和 Windows 冒烟观察记录；前端 fake 通知器测试只证明应用端口调用，不代表系统显示。
3. **点击定位**：点击事件携带 `schedule_id + original_date` 稳定身份。前端按 sequence 去重、切换到“待处理提醒”页并重新读取详情；查看、切换和激活都不自动确认。前端尚未就绪时只保留最新激活，退出后的残留通知不触发冷启动。
4. **确认持久化**：当前消息的确认按钮调用 `ConfirmReminder`，由 Service、MutationCoordinator 和 SQLite 事务提交；提交成功后才刷新待处理列表。重复点击被前端和后端共同保护，失败保留消息和详情以便重试。

待处理列表使用不透明游标；收到 `STALE_CURSOR` 时控制器自动回到第一页。详情状态 `pending`、`confirmed`、`missing` 和 `not_processable` 必须分别呈现；只有 `pending` 允许确认，内容不可用的已投递提醒仍可确认，但页面不得伪造正文。相同实际开始时间的提醒按稳定身份切换，确认成功后优先定位同刻下一条。

产品决定：系统通知逐条 Toast，不汇总为批量处理；退出应用后点击残留通知不冷启动；打开、切换或激活提醒不自动确认；确认只针对当前稳定身份，不影响其他消息和未来提醒。

设置页分别管理日志目录和备份目录。目录卡片同时显示当前进程实际使用的 `effective_path` 与下次启动使用的 `saved_path`；保存成功只提示“重启后生效”，不会在运行中替换 logger 或 SQLite Store。选择器取消、目录验证失败和配置原子保存失败均保留用户输入及旧的实际生效目录；页面没有数据库路径编辑入口。

验证命令：

```text
npm install
npm run typecheck
npm test
npm run build
```

Issue #31 的 Windows 冒烟验收还需分别记录：通知端口是否收到事件、Windows 通知中心是否实际显示、点击后现有窗口是否被激活并定位、确认后 SQLite 中 `processed_at_utc` 是否持久化，以及重启后待处理列表是否恢复。不得以 fake 通知器通过替代系统显示和点击观察。
