# TimeCue 贡献规范

本规范适用于 TimeCue 项目，并覆盖账号级 `.github` 仓库中的通用贡献规范。

## 分支策略

TimeCue 采用 `main + dev` 双主干模式：

- `main`：稳定分支，只用于版本级更新和正式发布，不作为日常开发分支。
- `dev`：日常开发集成分支，功能开发、Bug 修复、重构、文档和测试等工作应基于 `dev` 开始，并最终合并回 `dev`。

推荐关系：

`main ← dev ← feat/fix/refactor/docs/test/chore`

原则上：

- 不直接在 `main` 上进行日常开发。
- 普通开发分支不得直接提交 PR 到 `main`。
- 只有本仓库的 `dev` 分支可以向 `main` 提交版本发布 PR。
- 版本发布 PR 仅允许由授权发布负责人发起；当前授权人为 `dengweitian0-svg`。

## 日常开发流程

1. 创建 Issue，明确要解决的问题、需求或重构目标。
2. 更新本地 `dev` 分支。
3. 从 `dev` 创建开发分支。
4. 完成开发并提交代码。
5. 创建 Pull Request，目标分支为 `dev`。
6. 在 PR 中关联 Issue，例如：`Closes #12`。
7. CI 通过并完成自查后合并到 `dev`。
8. PR 合并后，由 GitHub 自动关闭关联 Issue。

最小闭环：

`Issue → Branch(from dev) → Commit → PR(to dev) → CI → Merge dev → Close Issue`

## 版本发布流程

当 `dev` 中的修改已经达到一个可发布版本，并完成必要验证后：

1. 由授权发布负责人创建 `dev → main` 的 Pull Request。
2. 确认版本内容和 CI / 手动验证结果。
3. 合并到 `main`。
4. 根据需要创建 Tag 和 GitHub Release。

当前授权发布负责人：

- `dengweitian0-svg`

版本发布链路：

`dev → Release PR → main → Tag / Release`

## 分支命名

推荐使用：

- `feat/<issue-number>-<short-name>`：新功能
- `fix/<issue-number>-<short-name>`：Bug 修复
- `refactor/<issue-number>-<short-name>`：重构
- `docs/<issue-number>-<short-name>`：文档
- `test/<issue-number>-<short-name>`：测试
- `chore/<issue-number>-<short-name>`：构建、工具链或维护任务

示例：

- `feat/12-recurring-reminder`
- `fix/18-missed-notification`
- `refactor/21-scheduler`

创建开发分支示例：

```bash
git switch dev
git pull
git switch -c feat/12-recurring-reminder
```

## Commit 规范

推荐使用 Conventional Commits：

- `feat: 添加周期提醒`
- `fix: 修复系统休眠后提醒丢失`
- `refactor: 拆分调度器模块`
- `docs: 更新架构说明`
- `test: 添加调度器测试`
- `chore: 更新开发工具配置`

如果需要加强 Issue 追踪，可以附带编号：

`feat: 添加周期提醒 (#12)`

## Pull Request 规范

### 日常开发 PR

- 来源：`feat/*`、`fix/*`、`refactor/*`、`docs/*`、`test/*`、`chore/*`
- 目标：`dev`
- 一个 PR 尽量只解决一个明确问题
- 应关联对应 Issue

### 版本发布 PR

- 来源：`dev`
- 目标：`main`
- 提出者：必须属于授权发布负责人名单
- 当前仅允许：`dengweitian0-svg`
- 用于发布已经在 `dev` 中集成并验证的版本
- 不应在发布 PR 中临时加入未经 `dev` 验证的新功能

## CI 规则

向 `dev` 提交 Pull Request 时，会自动执行 Go CI 检查。

当前检查包括：

- `gofmt`
- `go vet`
- `go test ./...`

CI 通过后再合并 PR。

## main 分支保护

`main` 只接受本仓库 `dev → main` 的版本发布 PR，并且 PR 提出者必须是授权发布负责人。

如果来源分支不正确，或 PR 提出者不在授权名单中，GitHub Actions 会自动留言并关闭该 PR。
