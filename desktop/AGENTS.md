# Flutter 桌面端

提供 Windows 房间场景与双页日程本。默认接入 Go 的查询和提醒确认，内存演示支持创建、编辑及删除交互。

- `lib/main.dart`：选择 HTTP 或演示仓储并启动应用。
- [lib/app](lib/app/AGENTS.md)：应用、主题与主屏装配。
- [lib/contracts](lib/contracts/AGENTS.md)：日程 DTO、草稿和仓储操作契约。
- [lib/data](lib/data/AGENTS.md)：HTTP 快照、分页、确认与内存示例数据。
- [lib/features/room](lib/features/room/AGENTS.md)：房间背景、时钟、桌面入口与本子显隐。
- [lib/features/notebook](lib/features/notebook/AGENTS.md)：本子开合、今日/日历页面、跨页选择与查询状态。
- [lib/features/schedule_editor](lib/features/schedule_editor/AGENTS.md)：日程表单、编辑草稿与时间选择器。
- [lib/theme](lib/theme/AGENTS.md)：纸张主题、视觉参数与通用页面组件。
- [assets](assets/AGENTS.md)：房间背景资源。
- [windows](windows/AGENTS.md)：Windows 原生窗口与 Flutter 构建集成。
- [test](test/AGENTS.md)：Widget、状态、HTTP 仓储与时间选择器测试。
- [tool](tool/AGENTS.md)：视觉截图工具。
- `README.md`：运行方式、接口说明与 Widget/状态索引。

工程约束：Widget/ViewModel 依赖仓储契约，具体实现由 `main.dart` 装配。真实模式通过 Go 查询与确认，不访问 SQLite、不在 Dart 展开 cron，创建/编辑/删除目前仅用于演示模式。结构变更同步 README 的 Widget 与状态索引。
