# Windows 发行候选包验证

日期：2026-10-12。开发位置：dev 工作区。refactor 已快进合并到本地 dev 的 10501fd；以下记录对应提交前的本地发布验证。验证期间未执行远程 CI 或创建 Release。

## 已执行

- Go 1.27.0 windows/amd64：`go vet ./...`、`go test -p 1 ./...` 全部通过，包含新库初始化、Flutter 旧库保留历史升级/备份、未知库拒绝及升级回滚测试；真实托管进程验证空库和旧 CHECK 启动成功。
- Flutter 既有同版本 Dart 源码的格式、分析及 177 项测试通过；本轮正式入口 `flutter build windows --release --no-pub --target=lib/main.dart` 构建成功。
- MSVC 19.51 / Visual Studio 2026：新建 release-native 构建目录，D7/D8 CTest 2/2 通过，包含新增默认运行路径与显式外连所有权验证；原生测试启用 /EHsc。
- `scripts/build-release.ps1 -SkipChecks -SkipFlutterBuild` 复用本轮已验证的正式 Flutter 构建，编译 stripped/trimpath Go，生成完整 ZIP 和 SHA256。清单注明源码工作区有未提交内容。
- 最终 ZIP 解压后的 23 个清单文件逐一 SHA256 校验通过，ZIP 自身校验通过。
- `release-smoke.py` 使用解压目录中的真实宿主和同目录 Go、新的中文空格数据路径，在第二次运行确认真实接口写入的日程仍存在；普通重复启动不更换运行身份，两次宿主强退后均通过持有句柄确认 Go 被 Job 回收。

## 本地产物

最终文件：`build/releases/TimeCue-0.1.0+1-windows-x64-20261012-010819-2348758.zip`，约 28 MB；同名 `.zip.sha256` 为压缩包校验值。

隔离验证记录：`build/releases/verify-final-20261012-0110/TimeCue 发行验证 xoq97eou/result.json`。测试数据库及日志保存在这个隔离目录中，不放入交付 ZIP。

## 验证边界

本机 Flutter 为 3.49.0-1.0.pre-240 master / Dart 3.14 开发版；CI 已固定 3.47.0 stable，但本轮未运行远程 CI。当前 ZIP 是未签名的本机发布候选包。

真实通知横幅/点击、Explorer 重启、休眠/关机、人工托盘明确退出和安装环境验证没有用上述自动检查代替。完整系统通知冷启动注册尚未实现；Wails 旧库自动转换明确不支持。正式对外发布继续以 RELEASE.md 的验收清单和 dev → main 流程为准。
