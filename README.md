# TimeCue

TimeCue 是一个 Windows 桌面提醒应用项目。

## 分支策略

项目采用 `main + dev` 双主干模式：

- `main`：稳定分支，只用于版本级更新和正式发布。
- `dev`：日常开发集成分支，功能开发、Bug 修复和重构都应基于 `dev` 进行。

推荐流程：

`Issue → 从 dev 创建开发分支 → Commit → Pull Request 到 dev → CI → Merge dev`

准备发布新版本时：

`dev → Pull Request → main → Release`

## 开发分支命名

- `feat/<issue-number>-<short-name>`
- `fix/<issue-number>-<short-name>`
- `refactor/<issue-number>-<short-name>`
- `docs/<issue-number>-<short-name>`
- `test/<issue-number>-<short-name>`
- `chore/<issue-number>-<short-name>`

示例：

`feat/12-recurring-reminder`
