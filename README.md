# TimeCue

用房间与日程本整理日常安排的 Windows 桌面提醒应用。

TimeCue 将单次安排、周期日程和待确认提醒放在同一个桌面空间中。你可以在日程本里查看今天的安排、回顾历史待确认事项，并通过 Windows 系统通知接收提醒。日程与提醒记录保存在本地 SQLite 数据库中。

## 功能

- **管理日常安排**：创建、编辑和删除单次或周期日程，支持每天、每周、每月重复，以及周期日程的暂停与恢复。
- **查看与确认提醒**：通过“今天”“历史待确认”“日历”查看安排，支持单条和批量确认；打开通知或详情不会自动确认。
- **补齐遗漏记录**：启动或运行恢复后整理遗漏提醒，并展示恢复进度与汇总。
- **融入 Windows 桌面**：提供系统通知、托盘与单实例唤回；关闭窗口后继续在托盘运行。

房间背景随早晨、下午和夜晚切换，日程本采用双页布局。

## 快速开始

当前仓库尚未发布 GitHub Release，可从源码运行。

### 从源码运行

准备以下环境：

| 依赖 | 要求 |
| --- | --- |
| 系统 | Windows x64 |
| Go | 1.27.0，与 main 分支的 go.mod 一致 |
| Flutter | 建议与 CI 一致，使用 3.47.0 stable |
| Windows 构建工具 | 支持 Flutter Windows 的 Visual Studio C++ 桌面开发工具链 |

在 PowerShell 中执行：

```powershell
git clone --branch main https://github.com/dengweitian0-svg/TimeCue.git
cd TimeCue
$timecueDataDir = (Get-Location).Path
cd desktop
flutter pub get
flutter run -d windows --debug -a --backend=managed -a "--data-dir=$timecueDataDir"
```

构建过程会编译 Go 服务，桌面端负责启动和关闭自有服务。上面的命令将开发数据放在克隆后的仓库目录中。

只体验界面与示例数据时，可在 desktop/ 目录运行：

```powershell
flutter run -d windows --dart-define=TIMECUE_DEMO=true
```

演示模式使用内存数据，不写入真实日程，也不发送真实系统通知。

## 数据与设置

日程、提醒历史、投递结果和确认状态保存在本地。Flutter 提供界面与 Windows 交互，Go 负责规则校验、调度和数据持久化。

在设置中，可以修改日志级别、日志目录和数据库目录。日志级别即时生效，目录更改在下次启动生效；修改目录不会自动搬迁已有数据。

更新发行包前，请先从托盘退出。随包提供的 update.py 支持校验文件并更新程序，运行它需要 Python 3.9 或更高版本。具体步骤与数据升级说明见[使用与更新](docs/RELEASE.md)。

## 开发与验证

Go 本地 HTTP 服务可单独启动。在仓库根目录执行：

```powershell
go run ./cmd/flutter --data-dir .
```

服务监听 http://127.0.0.1:10086，提供日程管理、提醒查询与确认等接口。健康检查路径为 /health。桌面端连接独立服务的方式见[桌面端说明](desktop/README.md)。

Go 检查，在仓库根目录执行：

```powershell
go vet ./...
go test -p 1 ./...
```

Flutter 检查，在 desktop/ 目录执行：

```powershell
dart format --output=none --set-exit-if-changed lib test
flutter analyze
flutter test
```

Windows 原生测试、更新脚本测试及完整检查流程见[贡献规范](docs/CONTRIBUTING.md)。

## 文档与贡献

- [桌面端说明](desktop/README.md)：运行模式、界面交互、通知与托盘。
- [使用与更新](docs/RELEASE.md)：发行包启动、更新与数据目录。
- [贡献规范](docs/CONTRIBUTING.md)：开发分支、Pull Request 和 CI 检查。
- [数据库迁移](migrations/README.md)：数据库初始化与旧数据兼容。

欢迎通过 [Issues](https://github.com/dengweitian0-svg/TimeCue/issues) 报告问题或提出建议。贡献代码请从 dev 创建独立分支，并向 dev 提交 Pull Request。

## 许可证

MIT
