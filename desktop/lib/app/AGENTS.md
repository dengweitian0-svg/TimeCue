# 应用宿主

- `timecue_app.dart` 装配应用界面、房间、聊天模型和运行门禁。
- `desktop_lifecycle.dart` 管理窗口、托盘、激活和退出。
- `managed_backend.dart` 管理自有 Go 进程、启动握手与健康检查。
- `windows_notification_bridge.dart` 连接平台通知与 Go 投递协议。
- `chat_runtime_guard.dart` 核验聊天操作的运行身份，`operation_toast.dart` 提供全局操作反馈。

## 工程约束

- 托管模式通过 ready 和 health 身份校验后开放业务。外连模式由外部管理 Go。
- 桌面清理共享收尾期限。发送停止消息后，自有 Go 有独立 65 秒退出期限，超时由 Windows Job 回收。通知回填绑定原运行身份和实际平台结果。
