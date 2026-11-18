# TimeCue

- [Go 服务入口](cmd/flutter/AGENTS.md)
- [HTTP API](internal/api/AGENTS.md)
- [应用编排](internal/app/AGENTS.md)
- [Agent](internal/agent/AGENTS.md)
- [HTTP 中间件](internal/middleware/AGENTS.md)
- [领域服务](internal/service/AGENTS.md)
- [并发协调](internal/syncutil/AGENTS.md)
- [业务模型](internal/model/AGENTS.md)
- [SQLite 存储](internal/store/AGENTS.md)
- [配置管理](internal/config/AGENTS.md)
- [日志](internal/log/AGENTS.md)
- [数据库迁移](migrations/AGENTS.md)
- [Flutter 桌面端](desktop/AGENTS.md)
- [构建与验收脚本](scripts/AGENTS.md)

## 工程约束

- 若存在 `.codex/AGENTS.md`，开始任务前须读取。按[贡献规范](docs/CONTRIBUTING.md)从 `dev` 创建开发分支。
- 代码注释使用中文，导出函数和方法须有中文注释。Go 结构体初始化含两个及以上字段时逐行书写，修改后运行 `gofmt`。
- 改动数据库表结构前须取得用户确认。
- 增删改 HTTP 接口后，须先创建并通过 Apifox 测试，再继续前端开发。
- 临时文件、执行报告和本地验收记录留在工作区，禁止提交或为其新增忽略规则。`docs/archify/` 中随图表交付的执行回执及校验、验收证据一并提交。
- 模块结构、职责归属或关键工程约束变化时，同步对应的 `AGENTS.md`。
