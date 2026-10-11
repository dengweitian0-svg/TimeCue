# GitHub 工作流

- `ci.yml`：面向 dev 的 PR 验证；Go 与 Flutter 均运行在 Windows runner。Go 使用 PowerShell 检查模块、格式、vet 和测试；Flutter 固定 3.47.0，检查格式、分析、测试并构建 Windows Release，通知、窗口和托盘插件版本由桌面 lockfile 固定；另构建并通过 CTest 执行 Windows 单实例/参数协议原生测试。
- `close-linked-issues.yml`：目标为 dev 的 PR 合并后，按关闭关键词处理关联 Issue。

约束：Go 与桌面目标平台保持一致。固定业务端口的跨包测试使用 `go test -p 1 ./...` 串行运行；应用构建与测试在 Windows 执行。工作流变更同步贡献规范及工程说明。

- 原生 CTest 统一构建 D7/D8 测试，除单实例与参数转交外覆盖自有进程、管道、Job 嵌套及崩溃清理；与 Dart 的托管启动/仓储/宿主回归共同阻止生命周期退化。

- Go 格式对受版本控制源码先统一 CRLF/LF，再逐文件比较 gofmt 标准输出；保留真实格式约束，避免 Windows checkout 换行导致全库伪失败，不在工作流改写源码。
