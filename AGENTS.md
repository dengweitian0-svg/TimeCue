# TimeCue

- `scripts/build-release.ps1` 打包完整 Flutter Windows x64 目录、timecue-core.exe、update.py、发行说明、版本/文件校验清单和 ZIP；发行说明见 docs/RELEASE.md。正式发行目录的主实例窗口启动成功后会登记当前用户 `TIMECUE_INSTALL_DIR`，供 Python 更新脚本定位安装目录。主实例默认 managed，使用当前用户 LocalAppData/TimeCue；Go 在监听前显式初始化新库或备份升级可识别 Flutter 旧库，拒绝自动转换 Wails 数据。

Windows 桌面日程提醒应用：Go 提供日程、提醒和本地数据服务，Flutter 提供房间与日程本界面。已接通查询、确认、规则操作、分钟调度、全期间恢复与汇总、任务成员分页及投递领取/开始/回填协议；Go 支持固定数据目录、运行身份、托管 ready 和父端停止控制。Flutter 已实现 Windows 即时通知桥、运行内结果重传、通知点击只读定位与恢复进度/成员展示；已接入托盘、关闭隐藏、原生单实例唤回与激活参数转交；Flutter 已接入自有 Go 的隐藏创建、身份握手、共享退出和 Job 崩溃清理。

- [cmd/flutter](cmd/flutter/AGENTS.md)：Go 本机 HTTP 服务启动、组件装配与退出清理。
- [internal/api](internal/api/AGENTS.md)：HTTP 路由、日程与提醒接口。
- [internal/agent](internal/agent/AGENTS.md)：Agent 聊天服务契约与内存 Mock。
- [internal/middleware](internal/middleware/AGENTS.md)：请求日志、Host 校验、异常恢复与响应适配。
- [internal/service](internal/service/AGENTS.md)：日程用例、触发时间计算、查询与确认、运行内投递尝试和重试。
- [internal/model](internal/model/AGENTS.md)：日程、提醒、查询条件及共享业务类型。
- [internal/store](internal/store/AGENTS.md)：SQLite 连接、健康检查、日程与提醒持久化。
- [internal/config](internal/config/AGENTS.md)：只读加载配置、发布快照、目录监听及运行路径解析；用户配置由 Flutter 写入。
- [internal/log](internal/log/AGENTS.md)：全局日志、JSONL 输出与级别热更新。
- [desktop](desktop/AGENTS.md)：Flutter 桌面界面、HTTP 数据接入和内存演示。
- [.github/workflows](.github/workflows/AGENTS.md)：PR 快速检查、dev 集成构建和 main 发布验证。

# 约束
- 如果项目中存在 `.codex/` 目录，开始任务前先读取其中的 `AGENTS.md`（若存在），并遵循其适用规则。
- 代码注释使用中文；可导出方法或函数必须添加中文注释；模块变更同步对应 AGENTS.md 和工程说明。Go 管理日程规则与提醒状态，Flutter 通过业务接口操作；平台副作用结果回传 Go。开发分支与检查流程见 [贡献规范](docs/CONTRIBUTING.md)
- 任何改动数据库表结构的操作必须获取请求
- 在新增，删除，更新 HTTP 接口后，必须要先在 Apifox 创建测试并通过后才能继续前端的开发
- AGENTS.md 文档中，只需要声明当前模块的组成，功能，约束即可，不记录决策
- 当前项目不需要过多的工程约束，简单够用即可
- 需要根据功能创建新分支来开发，而不是在 dev 分支中开发
- Flutter 已接入显式 managed/external 模式、隐藏自有 Go、ready/health 身份门禁和共享退出回填；Job Object 在进程创建时关联，最终回收及宿主崩溃只清理自有进程。开发外连不停止手动 Go，启动失败可重试、运行中异常退出不自动重启
- CI 按 PR 路径选择 Go、Flutter 或 Windows Python 更新脚本检查，纯文档 dev PR 只运行轻量分类；dev 非文档 push 后执行完整 Windows 集成验证；所有 main PR 通过 Go Windows Checks 和 Flutter Windows Checks 执行完整发布构建与原生验证；手动运行发行工作流时上传发行产物。
