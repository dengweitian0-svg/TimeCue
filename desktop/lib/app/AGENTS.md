# 应用装配

- `timecue_app.dart`：`TimeCueApp` 创建 MaterialApp 并应用主题和仓储注入；`DesktopShell` 装配房间、通知/桌面错误及重试入口，初始化应用级通知桥与桌面生命周期，按入口声明释放自有仓储。宿主呈现唯一只读通知弹窗，接纳就绪前目标、新目标复用窗口；桥识别新运行后使旧目标失效，释放展示模型及监听。明确退出与 Widget 释放共用一次运行资源清理；普通窗口关闭不释放应用。
- `desktop_lifecycle.dart`：宿主状态只读，初始化、窗口显隐、托盘恢复、激活及明确退出只经正式操作推进；窗口操作串行并按最新意图收敛，托盘必须经原生实际存在核验才允许隐藏。Windows 适配封装 window_manager/tray_manager 与 `timecue/desktop` 就绪/参数通道；窗口显示在原生线程同步完成并核验，避免 ShowWindowAsync 排队回执被误判为显示失败。窗口错误提供重试窗口，成功后只清除对应旧错误；独立托盘故障保留。启动缓冲只保留最新通知目标与普通打开意图；传输 ID 去重有界，D6 继续负责业务目标及健康身份校验。退出先停止新工作、尽力回填，再释放监听/仓储/托盘和销毁窗口；可注入 D8 自有进程停止，与回填共享总计 5 秒预算。
- `windows_notification_bridge.dart`：WindowsNotificationPlatform 封装插件初始化和即时展示；WindowsNotificationBridge 经投递窄契约推进单个不重叠循环，保存本运行的 attempt 去重、整数平台 ID、结果重传和 Go 身份代次；stopAndDrain 接受宿主剩余停止预算。

通知约束：Go 决定完整成员、冻结文案、次数与重试时间，Flutter 不重建资格和调度。shouldDisplay=false 不展示、不伪造 failed；平台结果已知后只重传原 outcome，未知结果不再次展示。新 attempt 批次及断线/404 后复查原 health，空领取不增加健康心跳。状态只放内存，身份切换不重置本 Flutter 运行的整数 ID 序号；已释放/旧代次的异步结果不发布状态。演示模式不装配真实通知；窗口隐藏不停止执行，点击回调不确认。共享仓储由宿主释放，通知桥不自行关闭 HttpClient。主实例资格由 runner 保持到进程结束，Flutter 不停止外连的手动 Go；明确退出后不自动重启。系统通知冷启动的完整 COM/进程链路不属于当前宿主已验证能力。
