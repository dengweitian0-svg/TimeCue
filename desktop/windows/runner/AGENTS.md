# Windows 应用入口

- `main.cpp`：初始化运行环境，在 Flutter 引擎创建前取得主实例资格或转交参数，然后创建 TimeCue 窗口并运行消息循环；mutex 所有权与句柄保持到退出清理完成。
- `single_instance.*`：按当前用户 SID/会话使用命名 mutex 和专用隐藏接收窗口；32 KiB 的版本化参数协议保留 UTF-8 与参数边界，来源仅为普通打开、显式通知目标及登录意图。转交总计等待最多 2 秒，校验用户/会话、协议及白名单；未就绪保存有界意图，退出拒绝新请求，崩溃只在重新取得所有权后接管。不以窗口标题、端口或持久锁文件决定实例资格。
- `flutter_window.*`：承载 Flutter 视图与插件注册；`timecue/desktop` 通道提供就绪/转交、实际托盘核验、同步显示完成、开始退出及销毁；`timecue/update` 返回构建版本、安装目录和运行目录，隐藏启动 PowerShell Prepare/Install 并等待 prepared/ready 状态，支持状态读取及安装提交/取消标记。更新进程不加入自有 Go Job。
- `desktop_window.h`：在拥有窗口的线程同步显示/恢复并核验实际可见性；不以异步排队请求或首次启动的隐藏标志裁定显示结果。
- `win32_window.*`：Win32 窗口和消息处理；关闭前保留宿主/插件拦截，真正销毁结束消息循环。
- `utils.*`：控制台与启动参数辅助功能。
- `Runner.rc`、`resource.h`、`resources/`、`runner.exe.manifest`：版本、图标及应用清单；`CMakeLists.txt`：runner 编译目标及单实例实现、Shell/用户身份系统库装配。

约束：二次启动不创建 Flutter 引擎、业务仓储、通知执行者或 Go。跨进程激活原样交给 D6 只读查询，不能更改正在运行的目录、连接或模式；显式 payload 参数不是实际 Windows 横幅点击证据。Windows 会话退出/进程崩溃可能直接结束运行，不承诺异步回填完成。原生测试位于 `desktop/test/native`，使用独立锁名及自有隐藏进程。

- `launch_configuration.*`：统一解析 backend/data-dir/go-executable 与 Windows 路径；单实例原始快照固定，重复启动显式冲突在接纳前拒绝，省略选项表示复用。转交到 Dart 的激活信封只包含原有窗口/通知意图。
- 主实例默认 managed、当前用户 LocalAppData/TimeCue 和程序目录中的 timecue-core.exe；只在取得主实例锁后补全默认值，转交端不把省略值变成冲突选项。显式 external 不创建或停止 Go；编译启用 UTF-8，系统目录解析链接 shell32/ole32。
- `managed_process.*`：单一所有者创建自有 Go；CreateProcessW/CREATE_NO_WINDOW、限定继承匿名管道及创建时 Job 列表，kill-on-close 且 Job 不继承；后台排空有界 stdout/stderr、观察退出。shutdown/EOF 优雅取消，Release 幂等回收管道、线程及句柄；timecue/process 通道由 FlutterWindow 接入，原生销毁也回收 Job。

约束：C++ 所有代码注释使用中文；每个函数仅保留一处用途、参数方向和返回值说明，优先放在头文件，文件内函数及回调在定义处说明；使用四空格缩进。
