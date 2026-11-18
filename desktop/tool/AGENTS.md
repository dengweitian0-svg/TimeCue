# 桌面联调与截图工具

- `capture_visuals_test.dart` 和 `capture_chat_visuals_test.dart` 生成桌面及聊天截图。
- `chat_markdown_smoke.dart` 验证 Markdown 的 Windows 构建依赖。
- `chat_real_http_smoke_test.dart` 和 `historical_http_smoke_test.dart` 通过真实 Go 核对聊天及历史待确认。
- `d8_host_smoke.dart`、`d8_host_smoke.py` 和 `d8_native_go_smoke.py` 验证宿主和原生进程管理。

## 工程约束

- 联调使用明确的隔离数据目录和实例身份。
