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

## PR #84 审查修复与接口门禁

Codex 的入口路径问题已修复为相对/绝对规范化比较。SDK 版本和渠道改从构建配置指向的 Flutter SDK 机器可读版本文件获取。存储健康检查要求提醒完整唯一键，单次时间在转换 Unix 纳秒前检查范围；既有分钟精度拒绝仍返回 400。

执行者退出协议为 POST /api/v1/notifications/executor-exit，包含当前 instanceId 及自身 attemptIds。Go 侧只清理精确匹配的活动尝试，保留预算并允许旧真实结果回填；Flutter 接入前已完成 Apifox 门禁。

Apifox 项目 8902401，隔离分支 ai/20261008-from-main-http-tests，环境 49486135；新增接口 523277220。9 个用例（420512431、420512556、420512565、420512571、420512600、420512611、420512682、420512688、420512695）共执行 11 次，全部通过，覆盖格式/身份拒绝、空与重复退出、旧退出不影响新 attempt、次数保留、迟到 sent 回流、9999 年与最大分钟后一分钟拒绝及最大合法分钟无损保存。证据保留在本地 .apifox/review84/validation-summary.json，不合并 Apifox 正式分支。

本轮修复后 Go vet 与无缓存全量测试通过；Flutter 格式、分析及 180 项全量测试通过。新增桥测试验证 start 在途、平台展示挂起和结果回填挂起时，外连退出仍在原共享预算内通知 Go，托管路径保持不发送该通知；HTTP 仓储测试验证退出请求载荷和端到端剩余超时。原生源码未变，发布 PR 的 CI 继续执行 D7/D8 全量检查。

## 验证边界

初次本地包使用 Flutter 3.49.0-1.0.pre-240 master / Dart 3.14 开发版。PR #84 的 45ed7d1 已通过 Flutter 3.47.0 stable Windows CI（运行 38193316487），但 Codex 随后提出新的 P2，因此该 CI 结果不替代本节修复后提交的再次验证。当前本地 ZIP 是未签名发布候选包。

真实通知横幅/点击、Explorer 重启、休眠/关机、人工托盘明确退出和安装环境验证没有用上述自动检查代替。完整系统通知冷启动注册尚未实现；Wails 旧库自动转换明确不支持。正式对外发布继续以 RELEASE.md 的验收清单和 dev → main 流程为准。
