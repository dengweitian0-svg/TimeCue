# 应用装配

- `timecue_app.dart`：`TimeCueApp` 创建 MaterialApp 并应用主题、仓储及可选配置文件存储注入；`DesktopShell` 将 main.dart 从启动配置取得的同目录 ConfigFileStore 传入房间设置入口，装配房间、通知/桌面错误及重试入口，初始化应用级通知桥与桌面生命周期，按入口声明释放自有仓储。宿主呈现唯一只读通知弹窗，接纳就绪前目标、新目标复用窗口；桥识别新运行后使旧目标失效，释放展示模型及监听。明确退出与 Widget 释放共用一次运行资源清理；普通窗口关闭不释放应用。外连退出将 500ms 保留给执行者退出回报，与已知结果 drain 共用宿主 5 秒预算；托管退出只 drain 后停止自有 Go。
- `desktop_lifecycle.dart`：宿主状态只读，初始化、窗口显隐、托盘恢复、激活及明确退出只经正式操作推进；窗口操作串行并按最新意图收敛，托盘必须经原生实际存在核验才允许隐藏。Windows 适配封装 window_manager/tray_manager 与 `timecue/desktop` 就绪/参数通道；窗口显示在原生线程同步完成并核验，避免 ShowWindowAsync 排队回执被误判为显示失败。窗口错误提供重试窗口，成功后只清除对应旧错误；独立托盘故障保留。启动缓冲只保留最新通知目标与普通打开意图；传输 ID 去重有界，D6 继续负责业务目标及健康身份校验。退出先停止新工作、尽力回填，再释放监听/仓储/托盘和销毁窗口；可注入 D8 自有进程停止，与回填共享总计 5 秒预算。
- `windows_notification_bridge.dart`：WindowsNotificationPlatform 封装插件初始化和即时展示；WindowsNotificationBridge 经投递窄契约推进单个不重叠循环，保存本运行的 attempt 去重、整数平台 ID、结果重传和 Go 身份代次；stopAndDrain 接受宿主剩余停止预算，并在外连退出时只回报本桥仍未完成的 attempt。

通知约束：Go 决定完整成员、冻结文案、次数与重试时间，Flutter 不重建资格和调度。shouldDisplay=false 不展示、不伪造 failed；平台结果已知后只重传原 outcome，未知结果不再次展示。新 attempt 批次及断线/404 后复查原 health，空领取不增加健康心跳。状态只放内存，身份切换不重置本 Flutter 运行的整数 ID 序号；已释放/旧代次的异步结果不发布状态。演示模式不装配真实通知；窗口隐藏不停止执行，点击回调不确认。共享仓储由宿主释放，通知桥不自行关闭 HttpClient。主实例资格由 runner 保持到进程结束，Flutter 不停止外连的手动 Go；明确退出后不自动重启。系统通知冷启动的完整 COM/进程链路不属于当前宿主已验证能力。

- `managed_backend.dart`：BackendConfiguration 读取 runner 的固定绝对路径快照并为配置存储提供同一目录；WindowsOwnedProcessPlatform 封装进程窄接口与代次事件。ManagedBackend 使用安全新身份、16 KiB 单行 ready、完整 health/PID/能力及存活核验，共享 10 秒启动截止；运行中退出使旧资格失效，不自动重启，零预算释放仍回收自有 Job。
- `timecue_app.dart` 的托管启动门禁只在双重核验后挂载 RoomScreen 和初始化通知桥；失败诊断可滚动并恢复可见窗口，激活仅缓冲最新目标。DesktopLifecycle 的 finalizeOwnedProcess 在所有优雅等待后无条件执行，再释放共享资源并销毁窗口。

- ManagedBackend 在创建每个异步启动操作前检查取消和截止，启动状态监听重入请求退出时不创建 Go；健康错配通过正式 invalidateRuntime 操作撤销整个托管运行。
- ManagedBackend 失败诊断保留有界 stderr；退出后同代次到达的输出仍补充可见错误，重试清空旧诊断。
- ManagedBackend.prepareStart 在创建 Go 前执行配置准备，与 ready/health 共用启动截止；准备失败进入可见失败和显式重试，取消或预算耗尽后不创建进程。main.dart 注入同一 ConfigFileStore 的旧数据库目录修正，外连及演示模式不执行该写入。

- `operation_toast.dart`：应用级操作结果提示宿主与 success/error/warning 入口。宿主包在 MaterialApp.builder 的 Navigator 外层，最多显示三条可关闭提示；每条单独计时，页面操作通过当前 BuildContext 发出反馈，未装配宿主时安全忽略。
