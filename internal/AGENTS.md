# internal

- 本目录包含 TimeCue 的内部应用模块，只允许项目内部按职责依赖；不得将高层业务逻辑下沉到基础模块。
- 当前结构：
  - [`bootstrap/`](bootstrap/AGENTS.md)：启动和退出生命周期。
  - [`config/`](config/AGENTS.md)：配置来源、解析和校验。
  - [`log/`](log/AGENTS.md)：结构化日志器构造。
  - [`runtime/`](runtime/AGENTS.md)：应用运行单元。
  - [`schedule/`](schedule/AGENTS.md)：日程领域规则、应用用例和时间环境。
  - [`storage/`](storage/AGENTS.md)：持久化适配器及其数据库实现。
  - [`utils/`](utils/AGENTS.md)：错误分类和退出码映射。
- 依赖方向必须保持单向：入口调用 `bootstrap`，`bootstrap` 编排 `config`、`log` 和 `runtime`，基础错误能力由 `utils` 提供；不得形成循环依赖。
- `bootstrap` 拥有顶层生命周期；`config` 不得承载业务流程，`log` 不得承载业务决策，`utils` 不得依赖具体业务模块。
- 新增 UI、存储、调度或领域模块时，必须放在职责明确的独立包中，并通过窄接口接入现有编排流程。
- 模块间传递错误应保留原始 cause 和必要上下文；最终错误记录和进程退出码处理必须遵循根目录规范。
