# TimeCue

用房间与日程本整理日常安排，也能用自然语言管理日程的 Windows 桌面提醒应用。

TimeCue 把单次安排、周期日程和待确认提醒放进同一本日程本。你可以查看今天的安排、回顾遗漏事项，也可以在 AI 页问「明天有什么安排？」或说「每周一上午 9 点提醒我开例会」。日程、提醒和聊天历史保存在本地 SQLite 数据库中，AI 对话请求发送到你配置的模型服务。

## 功能

- **管理日常安排**。创建、编辑和删除单次或周期日程，支持每天、每周、每月重复，以及周期日程的暂停与恢复。
- **查看与确认提醒**。在「今天」「历史待确认」「日历」查看安排，支持单条或批量确认。通知与详情打开后，提醒保持原来的确认状态。
- **补齐遗漏提醒**。应用启动或从运行中断恢复后整理遗漏提醒，可查看恢复进度和汇总。
- **融入 Windows 桌面**。支持系统通知、托盘和单实例唤回。关闭窗口后继续在托盘运行，房间背景随早晨、下午和夜晚切换。
- **在对话中管理日程**。AI 助手可以查询日程规则和指定日期的安排，也可以创建、修改和删除日程。对话支持流式回复、查看实际工具结果、停止生成，以及搜索和重开历史会话。

## 快速开始

### 从源码运行

| 依赖 | 要求 |
| --- | --- |
| 系统 | Windows x64 |
| Go | [go.mod](go.mod) 中声明的 1.27.3 |
| Flutter | 建议使用与 CI 一致的 3.47.0 stable |
| Windows 构建工具 | 支持 Flutter Windows 的 Visual Studio C++ 桌面开发工具链 |

在 PowerShell 中执行以下命令。

```powershell
git clone https://github.com/dengweitian0-svg/TimeCue.git
cd TimeCue
# 如需运行开发版本，在继续前执行 git switch dev。
$timecueDataDir = Join-Path (Get-Location).Path 'build\dev-data'
New-Item -ItemType Directory -Force -Path $timecueDataDir | Out-Null
cd desktop
flutter pub get
flutter run -d windows --debug -a --env=test -a --backend=managed -a "--data-dir=$timecueDataDir"
```

构建时会编译 Go 服务，由桌面端负责启动和退出。测试端口为 `1145`，开发数据保存在仓库的 `build/dev-data/` 中。

只想体验房间和双页日程本时，可在 `desktop/` 中运行以下命令。演示模式使用内存日程和示例数据，系统通知与 AI 对话在该模式下停用。

```powershell
flutter run -d windows --dart-define=TIMECUE_DEMO=true
```

### 构建 Windows ZIP

在仓库根目录执行以下命令。

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
```

ZIP 输出到 `build/releases/`。完整解压后双击 `timecue_desktop.exe`，桌面端会启动同目录的 `timecue-core.exe`。发行包默认使用程序目录保存数据，也可以通过 `--data-dir` 指定。启动、更新和数据目录详见[发行说明](docs/RELEASE.md)。

## 配置 AI 对话

点击房间里的猫咪或打开日程本的 AI 标签，选择「添加模型连接」。可以使用 DeepSeek 预设，也可以填写兼容 Chat Completions 的服务地址、模型 ID 和 API Key。在「管理模型」中可添加多个独立连接，为每个连接配置多个模型，并分别调整上下文和输出预算。保存并加载成功后即可切换模型，无需重启。

AI 对话需要可用的模型和密钥。日程、提醒和已有聊天历史可继续在本地访问。聊天支持上下文摘要与续写，重启后可查阅历史，并按已保存的进度恢复未完成的对话。配置文件、模型密钥和请求行为见[聊天配置与契约](docs/CHAT.md)。

## 数据与设置

Go 管理日程规则、提醒调度和 SQLite 持久化，Flutter 提供界面及 Windows 系统交互。首次运行会初始化版本 3 的数据库结构，可识别的 Flutter 旧库会先备份再升级。迁移范围和兼容性见[数据库迁移](migrations/README.md)。

在设置中可修改日志级别、日志目录和数据库目录。日志级别即时生效。目录更改在下次启动生效，已有文件保留在原位置。

更新发行包前请从托盘退出应用。随包提供的 `update.py` 会校验并替换程序文件，保留用户配置、数据库和日志。运行脚本需要 Python 3.9 或更高版本。操作步骤见[发行说明](docs/RELEASE.md#更新)。

## 开发与验证

可在仓库根目录单独启动 Go 本地 HTTP 服务，使用与桌面端相同的开发数据目录。

```powershell
go run ./cmd/flutter --env=test --data-dir .\build\dev-data
```

健康检查地址为 `http://127.0.0.1:1145/health`。省略 `--env` 或使用 `--env=release` 时端口为 `10086`。连接手动启动的 Go 时，桌面端使用 `--backend=external`，并指定同一环境与数据目录。两种模式的命令见[桌面端说明](desktop/README.md)。

Go 检查在仓库根目录执行。

```powershell
go vet ./...
go test -p 1 ./...
```

Flutter 检查在 `desktop/` 目录执行。

```powershell
dart format --output=none --set-exit-if-changed lib test
flutter analyze
flutter test
```

Windows 原生测试、更新脚本测试与 CI 规则见[贡献规范](docs/CONTRIBUTING.md)。

## 文档与贡献

- [文档索引](docs/INDEX.md) 汇总前端设计、领域术语与架构图。
- [Agent 架构与流程](docs/archify/architecture-agent-20261108-172843/README.md) 展示模型调用、日程工具和运行恢复。
- [提醒全流程](docs/archify/workflow-reminder-20261105-122427/README.md) 展示从规则保存、调度、系统通知到提醒确认的过程。

欢迎通过 [Issues](https://github.com/dengweitian0-svg/TimeCue/issues) 报告问题或提出建议。贡献代码请从 `dev` 创建独立分支，并向 `dev` 提交 Pull Request。

## 许可证

MIT
