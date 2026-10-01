# TimeCue

TimeCue 正在重构为面向 Windows 的桌面日程提醒应用。目标是由 Go 管理日程规则、提醒状态和本地数据，由 Flutter 提供桌面界面及系统交互。

> 当前仓库尚未形成可运行的桌面应用。Go 进程已提供仅供本机访问的日程 HTTP API；Flutter 界面仍未建立。

## 当前实现

| 部分 | 仓库中的现状 |
| --- | --- |
| 启动入口 | 加载配置、打开 SQLite、检查手动迁移结构，再监听 `127.0.0.1:10086`；收到退出信号后优雅关闭。 |
| 配置 | 从内嵌的 `internal/config/default.yaml` 加载默认值，以工作目录下的 `config.yaml` 覆盖；支持文件监听、配置快照、修改用户配置和恢复默认值。 |
| 日志 | 全局 `slog` 日志器按配置级别写入 `log.log_path/timecue.jsonl`；正式配置完成前的日志会被丢弃。 |
| 日程与提醒 | 已有创建日程和首条提醒的原子写入、下一次触发计算与提醒推进服务；HTTP API 支持创建、列表、查看及软删除日程，运行中的调度器尚未实现。 |
| SQLite | 已有日程与提醒读写、软删除、投递及确认状态更新；表结构由独立 SQL 迁移建立，入口检查迁移版本和表结构但不自动执行迁移。 |
| Flutter | 已确定为桌面前端方向；仓库中尚无 Flutter 工程和 Go／Flutter 通信实现。 |


## 产品目标与核心规则

- 创建、查看和删除单次或周期日程；周期日程允许暂停和恢复后续提醒。
- 每天、每周、每月是前端输入预设，保存时转换为五字段 cron 表达式。Go 端校验表达式并计算下一次触发时间，不为三种频率分别建立日程类型。
- 当前仅支持 `分钟 小时 * * *`、`分钟 小时 * * 星期列表`、`分钟 小时 日期列表 * *`。列表使用逗号；星期一至星期日为 `1–7`。不接受范围、步长、别名、月份限制或日期与星期混用。每月日期超出当月天数时在月末触发。
- 前端提供带 `+08:00` 偏移的北京时间，周期规则也按固定 UTC+8 解释；Go 端返回的业务时间统一带 `+08:00`，不采用夏令时。SQLite 仍以 Unix 纳秒保存时间点。可选的周期截止时间尚未实现。
- 持久化每次提醒的计划时间、投递结果和用户确认状态。展示通知、打开详情与用户确认是不同操作；外部通知结果须回传 Go 更新状态。
- Flutter 负责界面及 Windows 桌面交互，Go 负责日程用例、调度与 SQLite 状态转换。双方通过窄接口交换命令、查询结果和提醒事件。

重要状态只能经明确的业务操作改变。已发生的提醒应保留可查询的记录；周期表达式只决定未来触发时间，不代替提醒历史或确认状态。

## 配置

默认配置内嵌在 [`internal/config/default.yaml`](internal/config/default.yaml)。程序从**当前工作目录**读取 `config.yaml`；文件不存在时使用默认值。用户配置只需写需要覆盖的字段。例如：

```yaml
log:
  level: info
  log_path: ./log

db:
  db_path: ./db/timecue.db
```

`app.name` 和 `app.version` 也定义在默认配置中，属于应用元数据。相对路径目前按 Go 进程的工作目录解释；Windows 发布版的固定工作目录尚未接入启动流程。

`log.log_path` 是日志目录。程序加载配置后创建目录，并以追加模式写入其中的 `timecue.jsonl`；每行是一条 JSON 记录。如果日志文件无法打开，错误会输出到标准错误。

配置文件变动会触发重新解析，并以原子方式发布新的配置快照。**目前实际作用到运行组件的热更新只有 `log.level`**。`db.db_path` 的新值可以进入配置快照，但尚无数据库连接切换或数据迁移，因此不能把修改配置文件当作已搬迁数据库。后续的热更新范围限定为 `log.level` 和 `db.db_path`；`log.log_path` 计划只在启动时读取。

当前根目录的 `config.yaml` 还包含旧版的 `backup`、`window` 字段。现行配置结构不使用它们，也没有相应的备份或窗口行为。配置写入会保留已有的未知字段，但不会让这些字段生效。

## 本地 HTTP API

首次使用空数据库时，先按[迁移说明](migrations/README.md)手动执行 `001_initial.up.sql`，再从仓库根目录运行 `go run ./cmd/flutter`。服务固定监听 `http://127.0.0.1:10086`，仅接受 `Host: 127.0.0.1:10086`，不启用浏览器 CORS 或应用令牌。启动前检查数据库版本和表结构；`GET /health` 每次复查。缺少迁移时服务不会监听。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/health` | 数据库及迁移健康检查 |
| POST | `/api/v1/scheduler/schedules` | 创建单次或周期日程；请求体最大 1 MiB |
| GET | `/api/v1/scheduler/schedules` | 查询未删除日程；支持 `name`、`type`、`limit`、`offset` |
| GET | `/api/v1/scheduler/schedules/:id` | 查看未删除日程 |
| DELETE | `/api/v1/scheduler/schedules/:id` | 软删除日程，成功返回 204 |

创建请求示例：

```json
{"name":"工作日晨会","type":"period","description":null,"period":{"cron":["0 9 * * 1,2,3,4,5"]}}
```

单次日程使用 `"type":"single"` 和 `"single":{"notifyTime":"2026-10-10T09:30:00+08:00"}`；时间必须晚于创建时刻。周期日程创建时固定启用，不能传 `open`。两类规则不能同时出现，未知字段会被拒绝。成功响应使用 `{"data":{...}}`，创建返回 201 及 `Location`；列表返回 `{"data":[...]}`，按创建时间及 ID 降序排列。日程对象始终包含 `id`、`name`、`type`、`description`、`single`、`period`、`cancelled`、`createdAt`、`updatedAt`，不适用的规则字段为 `null`。业务时间带 `+08:00`。

查询参数 `type` 为 `single` 或 `period`，`limit` 默认为 50 且允许 1–50，`offset` 默认为 0 且必须非负。非法输入返回 400；不存在或已删除日程返回 404。普通错误格式为 `{"error":{"code":"...","message":"..."}}`。健康检查失败返回 503，错误位于 `data.error`。

## 开发与检查

当前 Go 模块要求 `go.mod` 所列的 Go 版本。在仓库根目录可以运行：

```powershell
go run ./cmd/flutter
```

这会启动本地 HTTP 服务，不会打开桌面窗口。SQLite 初始迁移的执行方法见 [`migrations/README.md`](migrations/README.md)；`Sqlite.Connect` 不会自动执行迁移。

需要检查现有 Go 代码时可运行：

```powershell
gofmt -l .
go vet ./...
go test ./...
```

仓库目前没有 Flutter 工程，因此还没有可用的 Flutter 开发或构建命令。现有 `.github/workflows/ci.yml` 仍包含指向不存在的 React／npm 前端目录的任务，需在 Flutter 工程建立时改为相应的检查。


分支与 Pull Request 流程见[贡献规范](docs/CONTRIBUTING.md)。其中的前端 CI 描述仍是旧版内容，待 Flutter 工程加入时同步更新。
