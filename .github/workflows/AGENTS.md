# GitHub 工作流

- `ci.yml`：面向 dev 的 PR 验证；Go 与 Flutter 均运行在 Windows runner。Go 使用 PowerShell 检查模块、格式、vet 和测试；Flutter 固定 3.47.0，检查格式、分析、测试并构建 Windows Release，通知插件版本由桌面 lockfile 固定。
- `close-linked-issues.yml`：目标为 dev 的 PR 合并后，按关闭关键词处理关联 Issue。

约束：Go 与桌面目标平台保持一致。固定业务端口的跨包测试使用 `go test -p 1 ./...` 串行运行；应用构建与测试在 Windows 执行。工作流变更同步贡献规范及工程说明。
