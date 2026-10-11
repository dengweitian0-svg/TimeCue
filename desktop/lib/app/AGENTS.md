# 应用装配

- `timecue_app.dart`：`TimeCueApp` 创建 MaterialApp 并应用主题和仓储注入；`DesktopShell` 装配房间、可见通知错误及连接重试入口，初始化应用级通知桥，退出请求先停止新展示并在 5 秒内尽力回填；按入口声明释放自有仓储。宿主呈现唯一只读通知弹窗，接纳就绪前目标、新目标复用窗口；桥识别新运行后使旧目标失效，释放展示模型及监听。
- `windows_notification_bridge.dart`：WindowsNotificationPlatform 封装插件初始化和即时展示；WindowsNotificationBridge 经投递窄契约推进单个不重叠循环，保存本运行的 attempt 去重、整数平台 ID、结果重传和 Go 身份代次；stopAndDrain 接受宿主剩余停止预算。

通知约束：Go 决定完整成员、冻结文案、次数与重试时间，Flutter 不重建资格和调度。shouldDisplay=false 不展示、不伪造 failed；平台结果已知后只重传原 outcome，未知结果不再次展示。新 attempt 批次及断线/404 后复查原 health，空领取不增加健康心跳。状态只放内存，身份切换不重置本 Flutter 运行的整数 ID 序号；已释放/旧代次的异步结果不发布状态。演示模式不装配真实通知；窗口隐藏不停止执行，点击回调不确认。共享仓储由宿主释放，通知桥不自行关闭 HttpClient。
