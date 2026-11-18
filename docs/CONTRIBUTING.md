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

## 日常开发流程

1. 创建 Issue，明确要解决的问题、需求或重构目标。
2. 更新本地 `dev` 分支。
3. 从 `dev` 创建开发分支。
4. 完成开发并提交代码。
5. 创建 Pull Request，目标分支为 `dev`。
6. 在 PR 中关联 Issue，例如：`Closes #12`。
7. CI 通过并完成自查后合并到 `dev`。
8. 目标为 `dev` 的 PR 实际合并后，由仓库的 `close-linked-issues.yml` 根据 PR 描述中的关闭关键词关闭关联 Issue。

最小闭环：

`Issue → Branch(from dev) → Commit → PR(to dev) → CI → Merge dev → Workflow Close Issue`

关闭 Issue 的自动化只由 PR 合并事件触发，且只处理目标为 `dev` 的已合并 PR；PR 描述需包含 `close`、`fix` 或 `resolve` 的有效变体以及 `#<issue-number>`。普通关闭 PR、目标为 `main` 的 PR 或未包含关闭关键词的 PR 不会触发关闭行为。

## 模块文档同步

项目级工程约束见[根 AGENTS.md](../AGENTS.md)。

- 模块结构、职责归属或关键工程约束变化时，同步对应的 `AGENTS.md`。父目录索引子模块，存在直属代码时补充该代码的职责。全局约束集中在根目录，模块专属约束就近维护。接口、运行行为和验证细节更新到对应的契约、工程说明或测试。

## 版本发布流程

Windows 完整包由 [build-release.ps1](../scripts/build-release.ps1) 构建，版本取自 `desktop/pubspec.yaml`。发布前按[发行说明](RELEASE.md)验证首次运行、重启数据、更新、迁移备份和系统交互。自动构建的触发条件见下方 CI 规则。

当 `dev` 中的修改已经达到一个可发布版本，并完成必要验证后：

1. 由授权发布负责人创建 `dev → main` 的 Pull Request。
2. 确认版本内容和 CI / 手动验证结果。
3. 合并到 `main`。
4. 根据需要创建 Tag 和 GitHub Release。

版本发布链路：

`dev → Release PR → main → Tag / Release`

## 分支命名

推荐使用：

- `feat/<issue-number>-<short-name>`：新功能
- `fix/<issue-number>-<short-name>`：Bug 修复
- `refactor/<issue-number>-<short-name>`：重构
- `docs/<short-name>`：文档
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
- 用于发布已经在 `dev` 中集成并验证的版本
- 不应在发布 PR 中临时加入未经 `dev` 验证的新功能

## CI 规则

| 触发条件 | 工作流 | 检查 |
| --- | --- | --- |
| `dev` PR | [ci.yml](../.github/workflows/ci.yml) | 按路径运行 Go、Flutter 和 Python 检查。纯文档只做分类。 |
| `main` PR | [ci.yml](../.github/workflows/ci.yml) | 完整检查，并构建 Windows Release、运行原生 CTest、打包。 |
| `dev` 非纯文档推送或手动触发 | [dev-integration.yml](../.github/workflows/dev-integration.yml) | Windows 集成构建、原生 CTest、验收脚本回归和发行包 smoke。 |
| 手动发布打包 | [release-package.yml](../.github/workflows/release-package.yml) | 构建并验证发行包，上传 ZIP 和校验值。发布负责人可按需创建 Tag 和 GitHub Release。 |

PR 分类在 Ubuntu 上运行，按文件路径选择检查。遇到未知非文档路径或文件数达到 3000 时运行全部检查。分支保护沿用 `Go Windows Checks` 和 `Flutter Windows Checks`，在 Windows 执行。路径跳过在 GitHub 中显示为 skipped。

- Go 使用 `gofmt`、`go vet ./...` 和 `go test -p 1 ./...`。测试使用固定端口，跨包串行运行。格式检查统一 CRLF/LF 后比较 `gofmt` 输出。
- Flutter CI 固定 stable 3.47.0，在 `desktop/` 运行依赖安装、格式检查、分析和测试。依赖更新时提交 lockfile。
- 更新脚本检查使用 Python 3.11，命令为 `python -m unittest discover -s scripts -p test_update.py -v`。`dev` PR 中仅变更 `scripts/` 下的 Python 文件时，只触发对应检查。
- 发行包 smoke 使用隔离数据验证空聊天会话、日程重启持久化、重复启动及自有 Go 的崩溃回收。遇到 `Low Mandatory Level` 文件标签时，在普通目录中重新解压候选包后验证。

## 本地运行

本地托管和外连命令见[Flutter 工程说明](../desktop/README.md)。本地联调显式使用 `--env=test` 和独立数据目录。切换后端模式或修改 Go 源码后，完整退出桌面宿主并重新启动。

## main 分支策略

`main` 只接受本仓库 `dev → main` 且由发布负责人 dengweitian0-svg 提出的版本发布 PR。protect-main.yml 对不满足条件的 PR 留言并关闭；所有 main PR 均由现有 `Go Windows Checks` 和 `Flutter Windows Checks` 执行完整验证，合法来源由 protect-main 管理。
