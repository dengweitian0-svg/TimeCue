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

- 如果更改了某个模块的内容，必须在对应模块的 `AGENTS.md` 中同步更新本次更改涉及的职责、结构、约束或行为说明；如果对应模块尚无 `AGENTS.md`，必须一并创建。

## 版本发布流程

Windows 完整包使用 `scripts/build-release.ps1`，版本来自 desktop/pubspec.yaml；产物包含完整 Flutter 目录及 timecue-core.exe、校验清单和 ZIP。dev PR 的 CI 保存同轮发行产物，手动 Windows Release Package 工作流用于稳定 SDK 全量验证和打包，不自动创建 Tag/Release。发布前按 docs/RELEASE.md 检查首次运行、重启数据、迁移备份和系统交互；本地 master SDK 通过不能替代 stable CI。

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

向 `dev` 提交 Pull Request 时，`.github/workflows/ci.yml` 的 Go 和 Flutter 检查均运行在 Windows runner，与应用目标平台一致。Go 工作流脚本使用 PowerShell；Flutter 工程位于 `desktop/`。

- Go：`gofmt`、`go vet ./...`、`go test -p 1 ./...`；固定业务端口的跨包测试串行运行。
- Flutter：CI 固定 Flutter 3.47.0，保留本机 master SDK；运行 `flutter pub get`、`dart format --output=none --set-exit-if-changed lib test`、`flutter analyze`、`flutter test`，通知插件及传递依赖提交 lockfile。
- Windows 构建：`flutter build windows --release`，默认连接手动启动的 Go 服务；在 desktop 执行 `cmake -S test/native -B build/desktop-native -A x64`、`cmake --build build/desktop-native --config Release` 和 `ctest --test-dir build/desktop-native -C Release --output-on-failure` 验证原生单实例、IPC、自有进程与 Job 清理。
- 本地运行：先在根目录运行 `go run ./cmd/flutter --data-dir .`，再在 `desktop/` 执行 `flutter run -d windows -a --backend=external -a --data-dir=<仓库绝对目录>`；视觉示例显式添加 `--dart-define=TIMECUE_DEMO=true`。
CI 通过后再合并 PR。

## main 分支策略

`main` 只接受本仓库 `dev → main` 的版本发布 PR，这是仓库的分支策略约束。当前 `.github/workflows` 中没有针对其他来源分支自动留言或关闭 PR 的 workflow；是否阻止不符合策略的 PR 由 GitHub 仓库设置和维护流程负责。

Windows CI 的 Go 格式检查会先统一 CRLF/LF 并比较 gofmt 输出；只消除换行差异，实际代码排版仍须一致，不格式化未修改的 Go 文件。
