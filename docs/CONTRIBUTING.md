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

Go 结构体初始化传入两个及以上字段时，字段必须逐行书写，不得挤在同一行；完成后使用 `gofmt` 格式化。

- 如果更改了某个模块的内容，必须在对应模块的 `AGENTS.md` 中同步更新本次更改涉及的职责、结构、约束或行为说明；如果对应模块尚无 `AGENTS.md`，必须一并创建。

## 版本发布流程

Windows 完整包使用 `scripts/build-release.ps1`，版本来自 desktop/pubspec.yaml；产物包含完整 Flutter 目录、timecue-core.exe、update.py、发行说明、校验清单和 ZIP。合并到 dev 后自动运行 Windows 集成构建与原生验证；目标为 main 的 PR 自动执行完整发布构建和原生验证；只有手动运行 Windows Release Package 才上传发行产物。工作流不自动创建 Tag/Release。发布前按 docs/RELEASE.md 检查首次运行、重启数据、更新、迁移备份和系统交互；本地 master SDK 通过不能替代 stable CI。

当 `dev` 中的修改已经达到一个可发布版本，并完成必要验证后：

1. 创建 `dev → main` 的 Pull Request。
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

向 `dev` 或 `main` 提交 Pull Request 时，`.github/workflows/ci.yml` 先在 Ubuntu 上通过 GitHub REST API 分类文件。dev PR 按变更运行 Go、Flutter 和 Python 检查；Go 与 Flutter 检查均在 Windows runner，与应用目标平台一致，Go 使用 PowerShell，Flutter 工程位于 `desktop/`。Python 更新脚本测试由 `Flutter Windows Checks` 在 Windows 上使用 Python 3.11 执行。纯文档 dev PR 只运行轻量分类；未知非文档路径或文件数达到 3000 时运行全部检查，分类失败时 Go 和 Flutter 检查直接失败。main PR 始终运行 Go、Flutter 和 Python 检查，并在 `Flutter Windows Checks` 中继续执行完整 Windows 发布构建与原生测试。非纯文档 dev push 后由 `dev-integration.yml` 运行完整 Windows 构建和原生测试；手动触发仍始终执行。

- Go：`gofmt`、`go vet ./...`、`go test -p 1 ./...`；Go 自动化测试监听 1145，跨包测试串行运行。
- Flutter：CI 固定 Flutter 3.47.0，保留本机 master SDK；运行 `flutter pub get`、`dart format --output=none --set-exit-if-changed lib test`、`flutter analyze`、`flutter test`，通知插件及传递依赖提交 lockfile。
- Python：`Flutter Windows Checks` 按 Python 路径条件设置 Windows Python 3.11，并在仓库根目录运行 `python -m unittest discover -s scripts -p test_update.py -v`，覆盖发行清单更新与安装路径登记逻辑及 Windows 文件共享锁/映像权限行为；纯 Python 改动不安装 Flutter。
- Windows 构建：完整发行构建、Go 服务打包和 CMake 原生生命周期测试由 dev push 集成工作流及 main PR 的 `Flutter Windows Checks` 执行；dev 普通 PR 不构建发行包。开发默认使用 managed；独立后端联调显式指定 backend=external。原生验证覆盖单实例、IPC、自有进程、Job 清理和安装目录登记。
- 本地运行：在 `desktop/` 执行 `flutter run -d windows --debug -a --backend=managed -a --data-dir=<仓库绝对目录>`，Flutter 会启动同目录 Go 服务并使用 1145；Profile 同样使用 1145。已有实例切换 backend 前需完整退出，Go 源码更改需退出并重新运行 Flutter，热重载不会重建 Go。独立后端联调可先在根目录运行 `go run ./cmd/flutter --data-dir=<仓库绝对目录> --port 1145`，再用 `flutter run -d windows --debug -a --backend=external -a --data-dir=<仓库绝对目录>` 启动；视觉示例显式添加 `--dart-define=TIMECUE_DEMO=true`。
`dev` 和 `main` 的 Required Checks 均使用现有 `Go Windows Checks` 和 `Flutter Windows Checks` 两项，无需配置新的检查名。dev 按路径选择检查，按路径跳过的 job 在 GitHub 中显示为 skipped，并视为成功；纯文档 dev PR 仍会运行轻量分类。main PR 不按路径跳过：Go job 执行 Go 检查，Flutter job 执行 Python、Flutter 检查、完整 Windows 发布构建和原生验证。Python 更新脚本检查由 `Flutter Windows Checks` 承载，无需新增 required check 名称。dev 集成工作流在合并后运行，失败时先修复或回退 dev 变更；纯文档 push 不触发集成构建。手动运行 `release-package.yml` 才会上传发行包。CI 通过后再合并 PR。

## main 分支策略

`main` 只接受本仓库 `dev → main` 且由发布负责人 dengweitian0-svg 提出的版本发布 PR。protect-main.yml 对不满足条件的 PR 留言并关闭；所有 main PR 均由现有 `Go Windows Checks` 和 `Flutter Windows Checks` 执行完整验证，合法来源由 protect-main 管理。

Windows CI 的 Go 格式检查会先统一 CRLF/LF 并比较 gofmt 输出；只消除换行差异，实际代码排版仍须一致，不格式化未修改的 Go 文件。
