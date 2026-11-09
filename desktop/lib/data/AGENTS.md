# 界面数据接入

- `http_notebook_repository.dart` 接入日程、提醒查询和投递，`http_chat_gateway.dart` 接入聊天 HTTP、事件分页和 SSE。
- `demo_notebook_repository.dart` 提供内存演示数据和交互。
- `config_file_store.dart` 负责用户配置文件读写及旧数据库目录规范化。
- `model_connection_config.dart` 管理通用模型连接，`deepseek_model_config.dart` 支持 DeepSeek 配置兼容。

## 工程约束

- 配置与密钥使用同一数据目录。保存走共享串行队列并保留无关字段。空 Key 按目标身份保留已有密钥。
- 公开设置只返回本地密钥配置状态，原始密钥留在文件存储边界。
