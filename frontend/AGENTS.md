# frontend

- 生产前端通过 `WailsPort` 使用窄端口；React 组件不得直接调用生成的 Wails bindings。
- `Controller` 拥有页面状态编排，不直接持久化配置；配置写入必须先成功返回，再更新本地投影。
- 主题只允许 `peach`、`mint`、`sky`、`lavender` 四个小写值。主题视觉值集中在 `src/themes.ts`，页面只消费语义 CSS 变量。
- 主题初始化失败时必须回退奶油桃并保留可观察错误；主题保存失败不得覆盖当前主题，重复选择当前主题不得产生写入。
- 原生标题栏同步只由 `Controller` 在主题读取、保存成功和恢复默认成功后触发；颜色来自 `themes.ts` 的 `surface/text`，同步失败不得回滚页面主题，只显示非阻断提示。
- 日程创建表单按单次、每天、每周、每月显示专用控件；重复表单使用隐藏的当前本地日期生成 `start_unix/end_unix/cron`，不提交截止日期，周规则支持多选且短月月规则取月末。
- 端口和适配器测试使用注入 fake，不伪造生成 Wails 模块；生产绑定只通过 `WailsAdapter` 接入。
- 前端修改完成后至少运行 `npm run typecheck` 和 `npm test`；构建相关变更再运行 `npm run build`。
