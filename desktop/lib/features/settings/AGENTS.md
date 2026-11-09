# 设置界面

- `settings_dialog.dart` 编辑日志与数据库配置。
- `model_config_dialog.dart` 编辑 Chat Completions 连接、模型和预算。

## 工程约束

- 通过宿主注入的存储读写配置。已保存和已加载分别确认，加载重试复用保存结果。
