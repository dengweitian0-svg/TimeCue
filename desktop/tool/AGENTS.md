# 视觉检查工具

- `capture_visuals_test.dart`：生成房间、日程本、编辑、日期/时间选择和确认弹层截图，输出到 `build/visuals/`。

- `chat_markdown_smoke.dart`：引用 `gpt_markdown` 和 NotebookTheme，提供混合中文、代码及表格的 Windows 发行构建依赖验证入口。

单次入口使用＋，日期与时间截图来自同一个左日历/右时间弹层；取消后重新打开＋，不依赖旧的两个圆角按钮。

`10-monthly-date-time.png` 对照每月的新左右布局：选中 31 日、22:09，左侧日号网格和右侧时间转盘同屏显示。

11-history-pending.png 保存历史待确认页、日期分组、共用选择工具栏及分页的实际 Widget 绘制。截图属于 Widget 证据，不替代 Windows 真机交互验收。

- historical_http_smoke_test.dart：手动连接指定身份的自有 Go 隔离进程，验证真实两范围分页、刷新保留位置、历史确认联动及非法集合拒绝。运行必须设置 TIMECUE_HISTORY_SMOKE_INSTANCE=history-flutter-smoke 并先核对 health 身份，不用于用户业务库。

- `d8_host_smoke.dart`：显式隔离数据目录中的 Windows release 联调入口，复用实际进程通道、托管协调、通知平台和宿主；父测试取得真实 Go 观察句柄后继续，核验 sent/未确认事实并请求共享明确退出。该入口不用于用户业务目录，正常发行构建仍使用 lib/main.dart。

- `d8_host_smoke.py` 配合测试 Dart 入口运行 managed/external 两种真实宿主闭环，保存新隔离目录、PID、sent/未确认及退出证据；`d8_native_go_smoke.py` 用 --go/--driver 的明确路径验证生产原生监管的真实 Go，覆盖优雅停止、EOF 和宿主崩溃。两者仅结束自身创建并持有句柄的测试进程。
