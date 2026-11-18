# 日程本

- `notebook_view_model.dart` 管理日历、待确认范围、分页及选择，`notebook_spreads.dart` 构建各标签页。
- `notebook_overlay.dart` 管理本子开合与编辑草稿守卫，`chat_spread.dart` 接入宿主聊天模型。
- `notification_view_model.dart`、`notification_reader_dialog.dart` 和 `notification_details.dart` 提供通知定位、成员分页和详情。
- `recovery_status.dart` 展示恢复进度，`schedule_management_dialog.dart` 提供规则管理入口。

## 工程约束

- 查看通知和恢复记录保持只读。确认提交明确选中的提醒 ID。
