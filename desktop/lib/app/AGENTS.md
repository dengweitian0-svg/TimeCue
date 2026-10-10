# 应用装配

- `timecue_app.dart`：`TimeCueApp` 创建 MaterialApp 并应用主题和仓储注入；`DesktopShell` 装配窗口内房间主屏。
- `windows_notification_bridge.dart`：NotificationPlatform 定义初始化、单次展示及释放；WindowsNotificationPlatform 通过固定通知插件与应用身份执行即时请求，使用 Go 提供的文案及身份 payload，不计算规则、成员或展示重试。
