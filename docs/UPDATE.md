# Windows 软件更新

TimeCue 的更新入口位于房间“设置”弹窗。Windows runner 从当前构建读取版本和安装目录；Flutter 只负责读取 Release 元数据、下载 ZIP 与 SHA-256 sidecar、组装更新计划和展示进度。`timecue/update` 原生通道负责隐藏启动随包 PowerShell 更新脚本，并在安装器确认等待后才允许应用进入退出流程。

## 可用范围

- Windows 托管模式可以检查和安装更新；Go 可执行文件必须是发行目录中的 `timecue-core.exe`。
- Windows external 模式允许检查版本，但不能安装。自定义 Go 路径、无效版本或缺少随包 `timecue-updater.ps1` 时禁用安装并显示原因。
- 发布源是仓库 GitHub Releases API。只接受非 draft、非 prerelease 的完整版本标签、唯一 Windows x64 ZIP 和匹配 `.sha256` sidecar。当前仓库为私有仓库，暂无正式 Release。本轮不处理 Release 发布与私有访问认证；当前客户端使用无需认证的 Release API，私有发行源仍需后续接入认证。
- 版本采用 `主.次.补丁+构建号` 四段数字比较；相同或低于当前版本的 Release 不提供安装。
- 演示模式不接入本地配置与更新安装。

## 安装流程

1. 设置入口创建独立更新控制器。检查按钮查询最新 Release，页面显示当前版本、最新版本和发行说明。正在检查、下载、校验、等待退出时禁用重复操作。
2. 用户确认安装和重启。若设置草稿未保存，先使用设置弹窗已有的确认守卫；取消时不会开始下载。
3. Flutter 下载完整发行 ZIP 和 sidecar，生成包含绝对路径、版本、数据目录、宿主 PID、自有 Go PID、重启参数、受保护路径和状态路径的 schemaVersion 1 计划。
4. 原生 runner 通过系统 Windows PowerShell 5.1 以 `CREATE_NO_WINDOW` 执行随包脚本 `-Mode Prepare -PlanPath <plan>`。脚本校验 ZIP sidecar、manifest、所有包内 SHA-256 和允许的目录结构，并把内容解包到暂存目录。Dart 仅在脚本退出码为零且工作区状态为 `prepared` 时继续。
5. 原生 runner 以相同隐藏方式启动 `-Mode Install`。安装器先打开 parent 与自有 Go 的进程句柄，写入 `ready` 状态并等待同目录 `commit.marker` 或 `cancel.marker`。只有就绪握手成功且用户确认后，Flutter 写入提交标记并调用共享 `DesktopLifecycle.requestExit()`；启动失败或退出请求错误会写入取消标记。
6. 安装器确认提交后等待两个受监控进程退出，再按 journal 事务替换程序文件、回滚失败变更并重启新版本。它不结束外连的手动 Go。

自动更新不会替换用户配置、数据库、SQLite `-wal`/`-shm` 文件、日志或更新状态。路径按实际配置加入计划的 `protectedPaths`，若与发行目录冲突，更新器拒绝安装。纯下载失败清理临时工作区；更新准备已启动后会保留计划与工作区诊断，进入安装器握手后继续保留暂存内容供取消、回滚与崩溃恢复使用。SQLite `file:` 数据源不能安全确定需保护的数据库路径，在线安装会拒绝继续。

## 状态和排错

稳定状态写入计划 `statusPath` 指定的 `<data-dir>\software-update-status.json`，包含状态、目标版本、更新时间和人可读诊断。默认目录为 `%LOCALAPPDATA%\TimeCue`；使用自定义 `--data-dir` 时状态也位于该目录。设置弹窗启动时读取状态文件并展示完成或失败信息；原生状态读取限制为 16 KiB。准备阶段和安装阶段的工作状态位于计划同目录的 `status.json`，提交/取消标记使用 `commit.marker` / `cancel.marker`。

如果新版本因程序文件问题无法启动，可先从托盘完整退出 TimeCue，并备份当前用户数据库及配置。再使用对应工作区中的更新脚本恢复程序文件：

```powershell
powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File <工作区\timecue-updater.ps1> -Mode Recover -JournalPath <status.journalPath>
```

恢复只回滚程序文件事务，不回滚数据库内容。若新版启动后已执行数据库迁移，回退程序时须同时遵循该版本已有的数据备份和兼容策略；更新器不会自动降级数据库。

检查不到 Release 时，设置显示“暂无可用的正式发行版本”。安装失败时保留用户数据和回滚日志，用户可从设置读取最近的诊断。Release 资产不匹配、校验和错误、目录越界、进程句柄无法打开、ready 超时和安装目录被保护数据占用都会拒绝继续。

## 验证边界

Widget 测试覆盖实际设置按钮、版本状态、external 安装禁用、重启确认和草稿守卫；客户端测试覆盖发布解析、下载顺序、安装启动错误、退出失败取消和重启后的状态恢复。`scripts/test-update.ps1` 在隔离临时目录验证 Windows 更新器的包安全、受保护路径、提交/取消、journal 回滚和重启参数。

真实跨版本验证需要用户发布正式 Release 后，在隔离 Windows 环境使用旧版安装目录执行。当前实现与隔离测试不代表真实 Release 上传、真实系统通知或正式版本跨升级已完成验收。

## 本轮本地验证

2026-10-13：Go vet 与串行全包测试通过；Dart 格式、Flutter analyze 与 223 个测试通过；Windows Release 构建和两个原生 CTest 通过；Windows PowerShell 5.1 更新器全套测试通过。Flutter 本机为 master 3.49.0-1.0.pre-240，stable CI 结果须独立确认。

完整包运行 `python scripts/update-smoke.py --package <发行目录>` 通过，覆盖真实 managed 启动与身份/父子 PID 校验、正式接口创建日程、损坏包拒绝、安装器 ready/commit、宿主硬退后的自有 Go Job 回收、完整文件哈希替换、重启新运行及自定义数据库/日志/配置保留、Recover 程序文件恢复。使用同一构建模拟旧版本号；不作为两个正式 Release 之间的升级或通知结果优雅回填真机证据。