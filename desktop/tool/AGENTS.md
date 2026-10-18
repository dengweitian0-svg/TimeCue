# 视觉检查工具

- `capture_visuals_test.dart`：生成房间、日程本、编辑、日期/时间选择和确认弹层截图，输出到 `build/visuals/`。

- `chat_markdown_smoke.dart`：引用 `gpt_markdown` 和 NotebookTheme，提供混合中文、代码及表格的 Windows 发行构建依赖验证入口。

- `chat_acceptance_fixture.dart`：供 Widget 测试和 Windows profile 共用的纯 Dart 网关夹具，提供两万字符以上混合 Markdown、多轮历史及按 run 隔离的可控事件；不代表真实模型或工具执行。
- `capture_chat_visuals_test.dart`：加载本机中文字体和打包的 Markdown 代码字体，生成欢迎、历史搜索、单双栏、过程状态、多工具、代码表格、停止、重连和失败截图；输出到 `build/visuals/chat-phase6/`，只作为 Widget 绘制证据。

- `chat_profile_smoke.dart`：使用纯 Dart 聊天验收夹具与生产 ChatViewModel、ChatSpread、NotebookFrame 和主题，执行长 Markdown 流式、历史、输入/停止、会话切换、滚动和原生窗口隐藏恢复检查；校准窗口外框以匹配实际 Flutter 客户区，并仅为自有测试窗口设置 2048×1400 的最大跟踪尺寸，窗口可能超出工作区；区分启动、尺寸过渡和稳态，以真实滚动动画收集每场景至少 20 帧；按引擎 frame number 关联 FrameTiming 并保留全部原始帧、marker 和未匹配诊断，启动帧前缀按明示 marker 证据单独归类；流式稳态独立统计。
- `run_chat_profile.py`：预检现有 TimeCue 窗口后启动已构建的 Windows profile smoke 程序，设置报告路径、限制运行时间并只清理自己启动的进程；不启动 Go、不访问数据库或用户实例。

运行步骤：在 `desktop` 目录先执行 `flutter build windows --profile --target=tool/chat_profile_smoke.dart`，再执行 `python tool/run_chat_profile.py`。工具输出到 `build/chat-profile/`；自动化使用 Flutter 指针事件和 TextEditingController，不覆盖 Windows 原生输入法、系统键盘/鼠标及 OS DPI/跨显示器人工验收。每个布局稳态和持续流式稳态至少采集 20 帧；16.667ms build/raster p95 是单独报告的帧目标，未达会显示诊断但不改变 exit code。exit code 仅表示功能、采样完整性和生产输入/发送/停止/切换/滚动 700ms 响应门槛；冷启动与 resize transition 单独报告，超时帧不丢弃；必需检查失败或 FlutterError 会使 profile 进程返回非零。

单次入口使用＋，日期与时间截图来自同一个左日历/右时间弹层；取消后重新打开＋，不依赖旧的两个圆角按钮。

`10-monthly-date-time.png` 对照每月的新左右布局：选中 31 日、22:09，左侧日号网格和右侧时间转盘同屏显示。

11-history-pending.png 保存历史待确认页、日期分组、共用选择工具栏及分页的实际 Widget 绘制。截图属于 Widget 证据，不替代 Windows 真机交互验收。

- historical_http_smoke_test.dart：手动连接指定身份的自有 Go 隔离进程，验证真实两范围分页、刷新保留位置、历史确认联动及非法集合拒绝。运行必须设置 TIMECUE_HISTORY_SMOKE_INSTANCE=history-flutter-smoke 并先核对 health 身份，不用于用户业务库。

- `d8_host_smoke.dart`：显式隔离数据目录中的 Windows release 联调入口，复用实际进程通道、托管协调、通知平台和宿主；父测试取得真实 Go 观察句柄后继续，核验 sent/未确认事实并请求共享明确退出。该入口不用于用户业务目录，正常发行构建仍使用 lib/main.dart。

- `d8_host_smoke.py` 配合测试 Dart 入口运行 managed/external 两种真实宿主闭环，保存新隔离目录、PID、sent/未确认及退出证据；`d8_native_go_smoke.py` 用 --go/--driver 的明确路径验证生产原生监管的真实 Go，覆盖优雅停止、EOF 和宿主崩溃。两者仅结束自身创建并持有句柄的测试进程。
