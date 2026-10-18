# 工程说明

- `CONTRIBUTING.md`：分支、贡献、检查及正式版本发布流程。
- `CONTRIBUTING.md` 同步说明 Debug/Profile 使用 1145、Release 使用 10086、独立后端端口联调及开发实例和默认数据目录隔离。
- `../.github/workflows/AGENTS.md`：GitHub Actions 工作流组成、职责和约束；按路径选择的 PR 检查、dev 集成验证、由既有 required checks 承载的 main 发布验证及手动发行包工作流说明与此文档保持同步。
- `DESIGN.md`：当前 Flutter 前端的视觉风格、基础参数、组件样式、信息层级、交互状态与 Windows 使用环境；包含房间猫咪入口、四标签、AI 双页/单栏、多会话历史搜索与删除、独立输入与阅读恢复、宿主失效状态、流式 Markdown 及工具名称展示，区分已实现规范和待完善建议，并同步主题颜色角色及已知对比度边界。
- `RELEASE.md`：完整 Windows ZIP 的启动、数据目录、托盘退出、安装路径登记、Python 更新步骤、构建和人工验收边界；作为发行包 README 面向用户。
- `RELEASE-VALIDATION.md`：本地发行候选包的实际自动检查、SDK、隔离验收证据和未验证项。
- `design/`：设计契约及各阶段实施验收，结构见其 AGENTS.md。
- 说明与当前实现同步；CI 路径分类和纯文档跳过规则、dev 非文档 push 后的集成构建、由既有 Go/Flutter checks 承载的 main PR 全量发布验证和手动产物入口按实际工作流说明；本地构建、自动验证和人工平台验收分别记录，不把未执行的 CI 或真实系统交互写为通过。
