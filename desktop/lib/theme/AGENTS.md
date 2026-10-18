# 视觉主题

- `notebook_theme.dart`：纸张与封面配色、文字和表单主题、统一房间入口按钮，以及 `PaperPage`、`PageHeading` 等通用页面组件。

工程约束：自定义颜色使用十进制 `Color.fromARGB`；表单、按钮、日期/时间选择和确认弹层共用纸张与暖棕主题，纸面主操作使用深棕色，封面小号文字放在纸色标签上并使用 ink；房间入口按钮最小高 44、圆角 12 且文字对比度至少 4.5:1，sage 用于完成事实标记。`desktop/test/chat_contrast_test.dart` 验证常用按钮、输入标签和封面标签的文字对比度。
