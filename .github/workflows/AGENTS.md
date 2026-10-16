# GitHub 工作流

- `ci.yml` 对 dev/main PR 执行路径分类，dev 仅运行受变更影响的检查；main 每次都运行 Go、Python、Flutter、Windows 发行构建和原生 CTest。纯文档 dev PR 只运行分类。`dev-integration.yml` 在非纯文档 dev push 或手动触发时执行完整 Windows 构建、发行打包和原生 CTest；`release-package.yml` 仅手动触发完整发布构建、原生验证并上传产物，不创建 Tag 或 GitHub Release。

- `ci.yml`：面向 dev 和 main 的 PR 检查；Ubuntu `changes` job 通过 GitHub REST API 分页读取 PR 文件，以当前路径和重命名前路径分类；纯文档 dev PR 只运行分类。文件数达到 3000 或出现未知非文档路径时运行全部检查；分类失败会使 Go 和 Flutter job 直接失败。Go 与 Flutter job 名称分别为 Go Windows Checks 和 Flutter Windows Checks，均运行在 Windows runner。Go 使用 PowerShell 检查模块、格式、vet 和串行测试；Flutter 固定 3.47.0，检查格式、分析和测试。Python 更新脚本测试由 Flutter Windows Checks 在 Windows 上使用 Python 3.11 执行；dev 按变更路径设置 Python/Flutter，main 始终执行两类检查并继续构建 Windows Release、运行原生 CTest、打完整发行包。Go job 最多 20 分钟，Flutter job 在 dev 最多 20 分钟、main 最多 60 分钟，分类最多 5 分钟。
- `ci.yml`：取消过期运行后，不继续启动 Go 或 Flutter 检查 job。
- `dev-integration.yml`：非纯文档 dev 分支 push 或手动触发时执行 Go、Flutter stable 3.47.0 的完整 Windows 发行构建和原生 CTest；手动触发始终运行；按工作流和 ref 隔离并发，每次最多 45 分钟。
- `release-package.yml`：仅手动触发完整 Windows 发行构建和原生 CTest，并上传 ZIP 与校验值。使用缓存，按 ref 隔离并发，每次最多 60 分钟。main 合法来源由 `protect-main.yml` 管理。
- `protect-main.yml`：main 发布 PR 只允许本仓库 dev 来源且作者为 dengweitian0-svg；不满足时留言并关闭 PR。
- `close-linked-issues.yml`：目标为 dev 的 PR 合并后，按关闭关键词处理关联 Issue。

约束：Go 与桌面目标平台保持一致。固定业务端口的跨包测试使用 `go test -p 1 ./...` 串行运行；应用构建与测试在 Windows 执行。PowerShell 中调用外部命令后检查 `$LASTEXITCODE` 并向工作流传递失败。Required Checks 沿用 `Go Windows Checks` 和 `Flutter Windows Checks`；main PR 发布验证始终在这两个 job 中全量运行。工作流变更同步贡献规范及工程说明。

- 原生 CTest 统一构建 D7/D8/D9 测试，除单实例与参数转交外覆盖自有进程、管道、Job 嵌套、崩溃清理及安装目录登记；与 Dart 的托管启动/仓储/宿主回归共同阻止生命周期退化。

- Go 格式对受版本控制源码先统一 CRLF/LF，再逐文件比较 gofmt 标准输出；保留真实格式约束，避免 Windows checkout 换行导致全库伪失败，不在工作流改写源码。
