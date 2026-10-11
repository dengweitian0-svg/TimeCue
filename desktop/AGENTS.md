# Flutter 桌面端

## 功能描述
- 提供 Windows 房间场景与双页日程本。默认接入 Go 的查询、确认及规则创建、编辑、启停和删除；内存仓储仅用于显式演示。
- 默认真实模式装配 Windows 即时通知桥，复用 Go 的领取、开始和回填协议；运行内去重和待回填结果不落盘，通知执行独立于本子开合及窗口焦点。点击定位、托盘、子进程及自启由后续功能接入。
- 房间与日程本书封固定提供管理入口，已暂停周期仍可查看完整信息；列表日程通过独立修改弹窗编辑、重新开启或删除，操作结束回到列表并刷新，不切换到日程本；查询规则列表不依赖日期投影。

- `lib/main.dart`：选择 HTTP 或演示仓储，真实模式装配单个通知执行者并启动应用。
- [lib/app](lib/app/AGENTS.md)：应用、主题与主屏装配。
- [lib/contracts](lib/contracts/AGENTS.md)：日程 DTO、草稿和仓储操作契约。
- [lib/data](lib/data/AGENTS.md)：HTTP 快照、分页、确认与内存示例数据。
- `lib/data/config_file_store.dart`：用户配置文件的 Flutter 写入入口，保留注释及其他字段，只管理运行配置覆盖值。
- [lib/features/room](lib/features/room/AGENTS.md)：房间背景、时钟、桌面入口与本子显隐。
- [lib/features/notebook](lib/features/notebook/AGENTS.md)：本子开合、今日/日历页面、跨页选择与查询状态。
- [lib/features/schedule_editor](lib/features/schedule_editor/AGENTS.md)：日程表单、编辑草稿与时间选择器。
- [lib/theme](lib/theme/AGENTS.md)：纸张主题、视觉参数与通用页面组件。
- [assets](assets/AGENTS.md)：房间背景资源。
- [windows](windows/AGENTS.md)：Windows 原生窗口与 Flutter 构建集成。
- [test](test/AGENTS.md)：Widget、状态、HTTP 仓储与时间选择器测试。
- [tool](tool/AGENTS.md)：视觉截图工具。
- `README.md`：运行方式、接口说明与 Widget/状态索引。

## 工程约束

- Widget/ViewModel 依赖仓储契约，具体实现由 `main.dart` 装配。真实模式通过 Go 正式用例读写，不访问 SQLite、不在 Dart 展开 cron；更新提交编辑时读取的 revision，写入重试使用同一操作标识。结构变更同步 README 的 Widget 与状态索引。
- `config.yaml` 的应用内写入归 Flutter 文件存储，使用与 Go 相同的 data-dir；Go 消费配置，不写用户配置。配置存储组件只读写 YAML，不迁移数据库或切换运行中的连接；设置页面和登录自启协调由对应功能装配。

日程本提供“今天｜历史待确认｜日历”三标签，今日安排与两类待确认分开读取。今日/历史各自分页与选择，房间总数继续包含全部到期未确认。跨北京日期废弃旧分页、重读两范围并清理旧范围选择；查询和标签切换只读，不调用平台投递。
