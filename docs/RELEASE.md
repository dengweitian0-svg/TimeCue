# TimeCue Windows 发行包

解压完整目录，双击 `timecue_desktop.exe`。不要只复制单个 exe；同目录的 `timecue-core.exe`、DLL 和 data 目录必须保留。

默认以托管模式运行 Go 服务，配置、数据库和日志保存到当前用户的 `%LOCALAPPDATA%\TimeCue`。安装目录只放程序和资源，移动或升级整个程序目录不会搬迁用户数据。首次启动自动建立数据库，之后重启保留日程与提醒。

设置中的数据库和日志均填写目录，例如 `D:\time_cue`；数据库自动补齐为 `D:\time_cue\timecue.db`，日志写入 `D:\time_cue\timecue.jsonl`。目录更改在下次启动生效，不搬迁已有数据库；新目录没有数据库时创建空库。config.yaml 仍位于 data-dir，其中 db.db_path 保存完整文件路径。

关闭窗口会收起到托盘，提醒继续运行。使用托盘“退出 TimeCue”才退出并回收自有 Go；明确退出后不继续实时提醒，再次启动会整理遗漏记录。

## 数据升级

已支持当前 Flutter 版本 1 到 2，以及版本 2 不含 loaded 状态约束的旧库升级。升级前 SQLite 在数据库旁生成 `.before-upgrade-时间.bak` 一致性备份；升级失败不提交结构转换。原 Wails 数据库和未知结构明确拒绝，保留原文件，不自动转换或覆盖。不要删除旧库来绕过错误。

可通过以下参数使用自定义目录：

```powershell
.\timecue_desktop.exe --data-dir="D:\TimeCue 数据"
```

开发外连需要明确指定模式和目录，并提前启动 Go：

```powershell
.\timecue_desktop.exe --backend=external --data-dir="D:\TimeCue 数据"
```

外连模式退出不停止手动 Go。服务固定使用 127.0.0.1:10086；占用时显示启动失败，不停止其他进程。

外连模式明确退出时，在同一退出预算内尽力回填已知结果并通知 Go 释放自身未完成的尝试，保留已用展示次数；迟到的旧退出不会影响新尝试。硬崩溃或断线无法承诺退出通知送达。启动健康检查同时核验提醒去重所依赖的完整唯一键，缺失时拒绝未知结构，不自动修复。

## 构建和验证

在仓库根目录运行 `powershell -ExecutionPolicy Bypass -File scripts/build-release.ps1`。版本取自 desktop/pubspec.yaml，输出位于 build/releases，包含 ZIP、SHA256 和包内文件清单。正式入口固定为 lib/main.dart，不使用演示或宿主测试入口。

正式发布使用 CI 固定的 Flutter 3.47.0 stable；本地 master SDK 构建用于本机验证，不能替代稳定 SDK 门禁。GitHub CI 对 dev PR 检查并打包，手动 Windows Release Package 工作流生成待发布产物；上传产物不等于创建正式 Release。

可运行 `python scripts/release-smoke.py --package <完整发行目录>` 验证新目录首次运行、中文路径、真实接口写入/重启保留、普通重复启动和宿主强退后的 Job 清理。脚本使用全新隔离目录并保存 result.json，端口占用时拒绝运行，不替代人工通知/退出验收。

人工验收必须在解压后的完整目录进行：

- 使用新的隔离数据目录首次启动、添加日程、退出并重启，确认数据保留。
- 核验中文与空格路径，隐藏窗口仍提醒、明确退出无自有进程遗留。
- 核验系统通知横幅、真实点击只读定位、Explorer 重启、休眠/恢复及关机。
- 核验旧 Flutter 数据保留与备份；未知或 Wails 数据库明确拒绝且原文件保留。

当前发行形式是未签名 ZIP，无安装器、自动更新或登录自启。通知插件尚无完整系统通知冷启动注册，应用已退出时的通知点击唤起不宣称已支持；普通启动和运行中通知定位沿用已有实现。正式对外发布前须完成上述人工系统验收，并明确这一功能边界。
