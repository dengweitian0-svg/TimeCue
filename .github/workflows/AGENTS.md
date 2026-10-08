# 工作流约定

- `ci.yml` 对目标为 dev 的 PR 保留 Go 检查，并在 Windows runner 中执行 desktop Flutter 格式、分析、测试和 Release 构建。
- Flutter 构建显式启用 TIMECUE_DEMO；阶段 A 产物不表示后端和平台生命周期能力已完成。
- 不重新引入不存在的 frontend/npm 构建任务。
