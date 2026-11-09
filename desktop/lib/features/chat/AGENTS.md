# 聊天功能

- `chat_view_model.dart` 管理会话索引、草稿、运行状态、事件消费和模型选择。
- `chat_content_segments.dart` 与 `chat_display_content.dart` 整理展示内容，`chat_markdown_body.dart` 渲染 Markdown。
- `chat_message_list.dart`、`chat_tool_call_list.dart` 和 `chat_session_list.dart` 展示消息、工具与会话。
- `chat_composer.dart` 提供输入、发送和停止操作。

## 工程约束

- ViewModel 通过聊天契约操作服务，模型身份使用稳定的 provider/model。运行终态与连接状态独立处理。
- 确认配置加载时核对完整的提供方模型集合及各模型的 `config_revision`。
