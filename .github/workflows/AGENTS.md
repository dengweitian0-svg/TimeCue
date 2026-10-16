# GitHub 工作流

- `ci.yml` 对 dev/main PR 执行 Go、Python、Dart 格式及静态检查和测试；`dev-integration.yml` 在 dev push 或手动触发时执行完整 Windows 构建、发行打包和原生 CTest；`release-package.yml` 对所有 main PR 执行完整发布构建和原生验证，仅手动触发时上传产物，不创建 Tag 或 GitHub Release。

- `ci.yml`：面向 dev 和 main 的快速 PR 检查；Go 与 Flutter job 名称分别为 Go Windows Checks 和 Flutter Windows Checks，均运行在 Windows runner。Go 使用 PowerShell 检查模块、格式、vet 和串行测试；Flutter 固定 3.47.0，检查格式、分析和测试，通知、窗口和托盘插件版本由桌面 lockfile 固定；Python 3.11 在仓库根目录运行发行更新脚本标准库测试。工作流按 PR 并发取消旧运行，每个检查最多 20 分钟。
- `dev-integration.yml`：dev 分支 push 或手动触发时执行 Python 3.11、Go、Flutter stable 3.47.0 的完整 Windows 发行构建和原生 CTest；按工作流和 ref 隔离并发，每次最多 45 分钟。
- `release-package.yml`：所有目标为 main 的 PR 执行完整 Windows 发行构建和原生 CTest；手动触发时额外上传 ZIP 与校验值。使用缓存，按 PR 或 ref 隔离并发，每次最多 60 分钟。main 合法来源由 `protect-main.yml` 管理。
- `protect-main.yml`：main 发布 PR 只允许本仓库 dev 来源且作者为 dengweitian0-svg；不满足时留言并关闭 PR。
- `close-linked-issues.yml`：目标为 dev 的 PR 合并后，按关闭关键词处理关联 Issue。

约束：Go 与桌面目标平台保持一致。固定业务端口的跨包测试使用 `go test -p 1 ./...` 串行运行；应用构建与测试在 Windows 执行。PowerShell 中调用外部命令后检查 `$LASTEXITCODE` 并向工作流传递失败。工作流变更同步贡献规范及工程说明。

- 原生 CTest 统一构建 D7/D8/D9 测试，除单实例与参数转交外覆盖自有进程、管道、Job 嵌套、崩溃清理及安装目录登记；与 Dart 的托管启动/仓储/宿主回归共同阻止生命周期退化。

- Go 格式对受版本控制源码先统一 CRLF/LF，再逐文件比较 gofmt 标准输出；保留真实格式约束，避免 Windows checkout 换行导致全库伪失败，不在工作流改写源码。
