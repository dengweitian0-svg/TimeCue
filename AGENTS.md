# TimeCue 项目约定

- Go 拥有日程规则、每次提醒及合法状态转换；Flutter 不直接访问业务数据库，平台副作用的结果必须回流。
- 桌面工程位于 desktop，阶段 B 默认连接手动启动的 Go HTTP 服务，显式 TIMECUE_DEMO=true 仍可运行内存示例，Widget 和状态关系见 desktop/README.md。
- 日常开发在 dev 派生分支进行，完整分支策略见 docs/CONTRIBUTING.md。
- 代码注释使用中文。修改模块时同步对应 AGENTS.md 和工程说明。
- 所有 git 操作优先使用 gh；gh 不支持的本地工作区检查使用 git。
- 架构按最小稳定点、删除/替换/拆分/重复测试确定职责；窄契约、单向依赖、状态通过明确操作变更，易变实现依赖稳定规则。

- 日程本是 Flutter 展示形式；Go 业务只使用 Schedule/Notification、原有仓储及 schedules/notifications 表，不增加独立本子模型或存储模块。
