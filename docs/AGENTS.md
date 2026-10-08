# 项目文档

- CONTRIBUTING.md 记录 main/dev 分支策略、模块文档同步和现有 CI 检查。
- 正式桌面视觉工程位于 desktop；Flutter 骨架与示例仓储的关系见 desktop/README.md。
- 设计提案与实现边界分别标注；Go 查询与确认已在阶段 B 接通，系统通知、托盘、规则变更和发布生命周期仍待实现。

- 阶段 B 查询复用 schedules 的 view/include/date/month/cursor 参数，确认统一使用 notificationIds 集合；工程说明和设计中的路由必须与实际注册保持一致。

- 后端查询和确认归入既有日程/提醒模块，持久化仅使用 schedules 和 notifications；日程本不对应独立数据库或业务实体。
