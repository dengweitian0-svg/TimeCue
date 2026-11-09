# 桌面测试

- `chat*_test.dart` 和 `http_chat_gateway_test.dart` 覆盖聊天界面、状态、契约和传输。
- `model*_test.dart`、`config_file_store_test.dart` 和 `settings_dialog_test.dart` 覆盖模型连接及运行配置。
- 日程本、日程编辑、提醒与历史测试覆盖查询、分页、确认、规则操作和界面交互。
- `managed*_test.dart`、`desktop_lifecycle_test.dart` 和 `windows_notification_bridge_test.dart` 覆盖宿主、托管和通知。
- `widget_test.dart`、`cat_chat_hotspot_test.dart` 和 `operation*_test.dart` 覆盖房间入口与操作反馈。
- `fixtures/` 提供共享测试夹具。[native](native/AGENTS.md) 提供 Windows 原生测试。

## 工程约束

- 真实 Go 用例使用隔离数据。合成聊天夹具限定在测试及截图工具中。
