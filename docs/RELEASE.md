# TimeCue Windows 发行说明

TimeCue 使用完整 Windows x64 目录运行。发行包包含 Flutter runner、Flutter 运行文件、同目录 `timecue-core.exe`、`timecue-updater.ps1`、说明文件及完整性清单。不要单独复制 `timecue_desktop.exe` 或删除发行目录中的组件。

首次启动默认以托管模式运行，并将配置、SQLite 数据库、日志和更新状态保存在当前用户的 `%LOCALAPPDATA%\TimeCue`。可使用 `--data-dir="D:\TimeCueData"` 指定数据目录。关闭窗口会隐藏到托盘；请从托盘菜单选择“退出 TimeCue”结束应用。external 模式仅用于开发或手动连接已有 Go 服务，退出应用不会停止该服务。

## 自动更新

设置页可查询 GitHub 上最新的正式 Release。external 模式可检查版本，但不能安装；安装只支持 Go 可执行文件与 TimeCue runner 位于同一发行目录的 managed 模式。更新前会要求确认重启；未保存的设置草稿会先要求确认舍弃。

更新器在替换程序前校验完整 ZIP 的 SHA-256 sidecar 和包内 manifest 文件哈希，并在隔离目录准备新版本。用户配置、数据库、SQLite WAL/SHM 文件、日志及更新状态属于受保护数据，不纳入替换。安装助手等待宿主和自有 Go 退出后才事务替换；失败时按 journal 回滚，状态写入实际 data-dir 下计划指定的 `software-update-status.json`。默认位置是 `%LOCALAPPDATA%\TimeCue\software-update-status.json`；显式指定 `--data-dir` 时状态随该目录。成功安装后恢复原有安全启动参数。

当前仓库为私有仓库，尚无正式 GitHub Release。本轮不处理 Release 发布与私有访问认证，没有创建 Tag 或 Release；当前客户端仅使用无需认证的 Release API。正式启用在线更新前，需提供公开发行源或补齐私有访问认证，再完成下列发布验收。

## 构建与发布

在仓库根目录执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build-release.ps1
```

脚本根据 `desktop/pubspec.yaml` 的版本构建 Flutter Windows x64 应用和 Go 服务，运行 Go 与 Flutter 检查，生成完整发行目录、ZIP 和 `.sha256` sidecar。默认输出到 `build/releases/`。本地构建成功不替代 Windows stable CI、隔离安装器测试和人工验收。

发布步骤：

1. 设置一个新的 `x.y.z+build` 版本，完成面向 `main` 的版本发布流程。
2. 在干净的 Windows x64 环境按上方命令生成正式包，核对 ZIP、SHA-256 sidecar 和 `manifest.json`。
3. 在仓库创建 `vX.Y.Z+build` 标签，并以该标签创建非 draft、非 prerelease 的 GitHub Release。
4. 上传唯一的 `TimeCue-X.Y.Z+build-windows-x64-YYYYMMDD-HHMMSS-<数字>.zip` 发行包，以及与之同名追加 `.sha256` 的文本文件。不要上传单个 exe 或不完整目录压缩包。
5. 在全新 Windows 环境验证首次启动和数据目录；再从旧版本验证下载、校验、安装、重启、用户数据保留以及失败回滚。

自动更新只接受版本标签与 ZIP 版本完全对应、且包含唯一 Windows x64 完整包及匹配 SHA-256 sidecar 的正式 Release。标签版本必须是 `v主.次.补丁+构建号`；更新客户端按四段数字比较，只提供高于当前版本的更新。Release 说明可用 Markdown 描述用户可见变化和升级注意事项。

## 首次运行与升级验收

- 在全新中文路径解压完整发行目录并启动，确认程序使用托管 Go，可创建和读取日程。
- 重启后确认配置、数据库和日志仍位于预期用户数据目录。
- 验证窗口关闭后可从托盘恢复，托盘菜单退出后宿主与自有 Go 均结束。
- 从较旧可识别数据版本升级时确认备份成功；对不可识别或不兼容数据应保留原文件并拒绝自动转换。
- 使用实际正式 Release 验证版本检查、ZIP 与 sidecar 下载、manifest 校验、安装器就绪/提交握手、安装重启、数据保护和失败回滚。
- 若更新状态为 failed/cancelled，检查 `%LOCALAPPDATA%\TimeCue\software-update-status.json` 中的中文诊断；重启后可从设置页查看最近状态。
- 对 SQLite `file:` 数据源，在线更新会拒绝安装；需要先使用普通数据库路径或 `:memory:`。

程序文件事务需要人工恢复时，先从托盘完整退出 TimeCue，并备份当前配置和用户数据库。使用发行/更新工作区保留的 `timecue-updater.ps1` 与状态文件中的 `journalPath` 执行：

```powershell
powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File <工作区\timecue-updater.ps1> -Mode Recover -JournalPath <status.journalPath>
```

Recover 只恢复安装程序文件，不会回滚数据库迁移。若新版本已经启动并迁移数据库，手动回退程序还须使用已有数据库备份和兼容策略；不要期待更新器自动降级数据库。

自动化检查、Windows 版本及尚未验证的系统行为应记录在对应验收报告中。没有实际运行的 CI、完整正式跨版本更新或人工验收不得标记为通过。
