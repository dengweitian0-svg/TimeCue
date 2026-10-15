# Flutter 桌面端

## 功能描述
- 提供 Windows 房间场景与双页日程本。默认接入 Go 的查询、确认及规则创建、编辑、启停和删除；日程本第四标签提供 AI 对话；内存仓储仅用于显式演示。
- 默认真实模式装配 Windows 即时通知桥及通知只读视图，复用 Go 的投递与查询协议；运行内去重和待回填结果不落盘，通知执行独立于本子开合及窗口焦点。点击校验运行身份并查看冻结成员，恢复入口显示本轮历史与进度；已接入托盘关闭隐藏、单实例唤回和激活参数转交，明确退出共用结果回填预算；自有 Go 子进程已接入；系统通知冷启动完整链路与自启由后续功能接入。
- 房间与日程本书封固定提供管理入口，已暂停周期仍可查看完整信息；列表日程通过独立修改弹窗编辑、重新开启或删除，操作结束回到列表并刷新，不切换到日程本；查询规则列表不依赖日期投影。

- `lib/main.dart`：选择 HTTP 或演示仓储，真实模式装配单个通知执行者、AI 对话网关及桌面生命周期；原生主实例资格先于仓储与执行者创建，通知 callback 经宿主唤回后进入只读定位。
- [lib/app](lib/app/AGENTS.md)：应用、主题与主屏装配。
- `lib/app/operation_toast.dart`：应用级操作结果 Toast 宿主及成功、失败、警告入口。
- [lib/contracts](lib/contracts/AGENTS.md)：日程 DTO、草稿和仓储操作契约。
- [lib/data](lib/data/AGENTS.md)：HTTP 快照、分页、确认与内存示例数据。
- `lib/data/config_file_store.dart`：用户配置文件的 Flutter 写入入口，保留注释及其他字段，只管理运行配置覆盖值。
- [lib/features/room](lib/features/room/AGENTS.md)：房间背景、时钟、桌面入口与本子显隐。
- [lib/features/notebook](lib/features/notebook/AGENTS.md)：本子开合、今日/日历页面、跨页选择与查询状态。
- [lib/features/schedule_editor](lib/features/schedule_editor/AGENTS.md)：日程表单、编辑草稿与时间选择器。
- [lib/features/settings](lib/features/settings/AGENTS.md)：日志与数据设置弹窗、配置保存与恢复默认。
- [lib/theme](lib/theme/AGENTS.md)：纸张主题、视觉参数与通用页面组件。
- [assets](assets/AGENTS.md)：房间背景资源。
- [windows](windows/AGENTS.md)：Windows 原生窗口与 Flutter 构建集成。
- [test](test/AGENTS.md)：Widget、状态、HTTP 仓储与时间选择器测试。
- [tool](tool/AGENTS.md)：视觉截图工具。
- `README.md`：运行方式、接口说明与 Widget/状态索引。

## 工程约束

- Widget/ViewModel 依赖仓储契约，具体实现由 `main.dart` 装配。真实模式通过 Go 正式用例读写，不访问 SQLite、不在 Dart 展开 cron；更新提交编辑时读取的 revision，写入重试使用同一操作标识。结构变更同步 README 的 Widget 与状态索引。
- `config.yaml` 的应用内写入归 Flutter 文件存储，使用与 Go 相同的 data-dir；Go 消费配置，不写用户配置。房间设置弹窗编辑日志级别、日志目录及数据库目录，数据库文件名 timecue.db 自动补齐，支持恢复默认和草稿守卫；路径下次启动生效，不迁移数据库或切换运行中的连接。演示模式禁用设置写入，登录自启协调由对应功能装配。

日程本提供“今天｜历史待确认｜日历｜AI 对话”四标签，今日安排与两类待确认分开读取。今日/历史各自分页与选择，房间总数继续包含全部到期未确认。跨北京日期废弃旧分页、重读两范围并清理旧范围选择；查询和标签切换只读，不调用平台投递。

AI 对话通过猫咪入口或日程本第四标签打开；对话网关只在真实模式装配，演示模式明确禁用入口。会话模型由应用宿主持有，关闭本子或隐藏窗口不停止已有 run；应用退出或托管运行失效时封锁后续请求并停止事件流监听。

- 真实启动默认 managed、当前用户 LocalAppData/TimeCue 及同目录 timecue-core.exe，可显式覆盖路径。开发外连显式指定 backend=external 和 data-dir。主实例握手前不挂载业务房间或执行通知，失败保留可见错误/重试/退出；隐藏不停止自有 Go，退出回填与 Go 协作共用 5 秒预算，零预算仍最终关闭 Job。
- Windows Flutter 构建要求 Go 已安装并位于 PATH；平台构建每次编译 Go 服务并安装到 runner 同目录，使默认 managed 开发启动可直接创建自有服务。切换 managed/external 前需完整退出旧实例，Go 源码更改需退出并重新运行 Flutter，热重载不重建 Go。
- 托管启动在创建 Go 前修正旧设置保存的数据库目录值，自动追加 timecue.db；设置只读视图显示原目录，不误取父目录。配置修正失败走启动错误/重试，不创建 Go 或迁移数据库。
