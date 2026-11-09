# Flutter 应用代码

- `main.dart` 装配运行模式、仓储、通知与聊天服务。
- [app](app/AGENTS.md) 管理应用宿主和桌面生命周期。
- [contracts](contracts/AGENTS.md) 定义 DTO 与数据端口。
- [data](data/AGENTS.md) 实现 HTTP、文件和演示数据接入。
- [room](features/room/AGENTS.md) 提供房间和功能入口。
- [notebook](features/notebook/AGENTS.md) 提供日程本页面与通知详情。
- [chat](features/chat/AGENTS.md) 提供聊天状态和交互组件。
- [schedule_editor](features/schedule_editor/AGENTS.md) 提供日程表单与时间选择。
- [settings](features/settings/AGENTS.md) 提供运行和模型设置。
- [theme](theme/AGENTS.md) 提供共享主题和基础组件。

## 工程约束

- 正式模式通过 Go 接口操作日程与提醒，平台投递结果回传 Go。演示仓储由入口显式装配。
