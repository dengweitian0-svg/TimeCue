# 视觉检查工具

- `capture_visuals_test.dart`：生成房间、日程本、编辑、日期/时间选择和确认弹层截图，输出到 `build/visuals/`。

单次入口使用＋，日期与时间截图来自同一个左日历/右时间弹层；取消后重新打开＋，不依赖旧的两个圆角按钮。

`10-monthly-date-time.png` 对照每月的新左右布局：选中 31 日、22:09，左侧日号网格和右侧时间转盘同屏显示。

11-history-pending.png 保存历史待确认页、日期分组、共用选择工具栏及分页的实际 Widget 绘制。截图属于 Widget 证据，不替代 Windows 真机交互验收。

- historical_http_smoke_test.dart：手动连接指定身份的自有 Go 隔离进程，验证真实两范围分页、刷新保留位置、历史确认联动及非法集合拒绝。运行必须设置 TIMECUE_HISTORY_SMOKE_INSTANCE=history-flutter-smoke 并先核对 health 身份，不用于用户业务库。
