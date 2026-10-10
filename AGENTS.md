# TimeCue

Windows 桌面日程提醒应用：Go 提供日程、提醒和本地数据服务，Flutter 提供房间与日程本界面。已接通查询、确认、规则操作、分钟调度、全期间恢复与汇总、任务成员分页及投递领取/开始/回填协议；Windows 通知执行者和恢复界面待后续接入。

- [cmd/flutter](cmd/flutter/AGENTS.md)：Go 本机 HTTP 服务启动、组件装配与退出清理。
- [internal/api](internal/api/AGENTS.md)：HTTP 路由、日程与提醒接口。
- [internal/middleware](internal/middleware/AGENTS.md)：请求日志、Host 校验、异常恢复与响应适配。
- [internal/service](internal/service/AGENTS.md)：日程用例、触发时间计算、查询与确认、运行内投递尝试和重试。
- [internal/model](internal/model/AGENTS.md)：日程、提醒、查询条件及共享业务类型。
- [internal/store](internal/store/AGENTS.md)：SQLite 连接、健康检查、日程与提醒持久化。
- [internal/config](internal/config/AGENTS.md)：只读加载配置、发布快照、目录监听及运行路径解析；用户配置由 Flutter 写入。
- [internal/log](internal/log/AGENTS.md)：全局日志、JSONL 输出与级别热更新。
- [desktop](desktop/AGENTS.md)：Flutter 桌面界面、HTTP 数据接入和内存演示。

# 约束
- 代码注释使用中文；模块变更同步对应 AGENTS.md 和工程说明。Go 管理日程规则与提醒状态，Flutter 通过业务接口操作；平台副作用结果回传 Go。开发分支与检查流程见 [贡献规范](docs/CONTRIBUTING.md)。
- 任何改动数据库表结构的操作必须获取请求
- 在新增，删除，更新 HTTP 接口后，必须要先在 Apifox 创建测试并通过后才能继续前端的开发
- AGENTS.md 文档中，只需要声明当前模块的组成，功能，约束即可，不记录决策
- 当前项目不需要过多的工程约束，简单够用即可
