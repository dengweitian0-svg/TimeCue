# 工程说明

- `CONTRIBUTING.md`：分支、贡献、检查及正式版本发布流程。
- `../.github/workflows/AGENTS.md`：GitHub Actions 工作流组成、职责和约束；按路径选择的 PR 检查、dev 集成验证、由既有 required checks 承载的 main 发布验证及手动发行包工作流说明与此文档保持同步。
- `DESIGN.md`：当前 Flutter 前端的视觉风格、基础参数、组件样式、信息层级、交互状态与 Windows 使用环境；区分已实现规范和待完善建议。
- `RELEASE.md`：完整 Windows ZIP 的启动、数据目录、托盘退出、安装路径登记、Python 更新步骤、构建和人工验收边界；作为发行包 README 面向用户。
- `RELEASE-VALIDATION.md`：本地发行候选包的实际自动检查、SDK、隔离验收证据和未验证项。
- `design/`：设计契约及各阶段实施验收，结构见其 AGENTS.md。
- 说明与当前实现同步；CI 路径分类和纯文档跳过规则、dev 非文档 push 后的集成构建、由既有 Go/Flutter checks 承载的 main PR 全量发布验证和手动产物入口按实际工作流说明；本地构建、自动验证和人工平台验收分别记录，不把未执行的 CI 或真实系统交互写为通过。
