# TimeCue

TimeCue 正在重构为面向 Windows 的桌面日程提醒应用。目标是由 Go 管理日程规则、提醒状态和本地数据，由 Flutter 提供桌面界面及系统交互。

> 当前可运行 Flutter 视觉骨架和独立 Go 本机 HTTP API。阶段 B 已接通查询与确认，Go 已完成规则事务更新；系统通知、托盘及发布生命周期仍待实现。

## 当前实现

| 部分 | 仓库中的现状 |
| --- | --- |
| 启动入口 | 加载配置、打开 SQLite、检查手动迁移结构，再监听 `127.0.0.1:10086`；收到退出信号后优雅关闭。 |
| 配置 | 从内嵌的 `internal/config/default.yaml` 加载默认值，以工作目录下的 `config.yaml` 覆盖；支持文件监听、配置快照、修改用户配置和恢复默认值。 |
| 日志 | 全局 `slog` 日志器按配置级别写入 `log.log_path/timecue.jsonl`；正式配置完成前的日志会被丢弃。 |
| 日程与提醒 | 已有创建日程和首条提醒的原子写入、下一次触发计算与提醒推进服务；HTTP API 支持日程基础操作、日期/月历/历史待确认查询和原子单条/批量确认，运行中的调度器尚未实现。 |
| SQLite | 已有日程与提醒读写、软删除、投递及确认状态更新；表结构由独立 SQL 迁移建立，入口检查迁移版本和表结构但不自动执行迁移。 |
| Flutter | `desktop/` 已实现三时段房间、双页本子及创建/编辑视觉骨架，默认接入 Go 查询与单条/批量确认，视觉示例仍用显式内存仓储。 |


## 产品目标与核心规则

- 创建、查看和删除单次或周期日程；周期日程允许暂停和恢复后续提醒。
- 每天、每周、每月是前端输入预设，保存时转换为五字段 cron 表达式。Go 端校验表达式并计算下一次触发时间，不为三种频率分别建立日程类型。
- 当前仅支持 `分钟 小时 * * *`、`分钟 小时 * * 星期列表`、`分钟 小时 日期列表 * *`。列表使用逗号；星期一至星期日为 `1–7`。不接受范围、步长、别名、月份限制或日期与星期混用。每月日期超出当月天数时在月末触发。
- 前端提供带 `+08:00` 偏移的北京时间，周期规则也按固定 UTC+8 解释；Go 端返回的业务时间统一带 `+08:00`，不采用夏令时。SQLite 仍以 Unix 纳秒保存时间点。可选的周期截止时间尚未实现。
- 持久化每次提醒的计划时间、投递结果和用户确认状态。展示通知、打开详情与用户确认是不同操作；外部通知结果须回传 Go 更新状态。
- Flutter 负责界面及 Windows 桌面交互，Go 负责日程用例、调度与 SQLite 状态转换。双方通过窄接口交换命令、查询结果和提醒事件。

核心业务持久化使用 `schedules` 和 `notifications`，阶段 C 的 `schedule_versions` 与 `schedule_write_receipts` 保存规则区间和幂等回执；Go 查询与确认归入原有日程和提醒模块。Flutter 的日程本是展示方式，没有独立数据库、表或后端业务模型。

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

日志的 `time` 字段统一使用固定 UTC+8 北京时间，格式为 `YYYY-MM-DD-HH:MM:SS`（例如 `2026-10-01-09:30:00`），精确到秒，不受主机时区影响。

HTTP 请求由统一日志中间件每次最多记录一条：日程与提醒查询成功使用 Debug，创建、更新、删除及提醒确认成功使用 Info，拒绝使用 Warn，内部失败使用 Error。提醒列表记录本页数量和总数，详情记录提醒及日程 ID，确认记录去重后返回条数；不记录名称、说明、请求体、响应体、查询字符串或完整批量 ID 集合。

配置文件变动会触发重新解析，并以原子方式发布新的配置快照。**目前实际作用到运行组件的热更新只有 `log.level`**。`db.db_path` 的新值可以进入配置快照，但尚无数据库连接切换或数据迁移，因此不能把修改配置文件当作已搬迁数据库。后续的热更新范围限定为 `log.level` 和 `db.db_path`；`log.log_path` 计划只在启动时读取。

当前根目录的 `config.yaml` 还包含旧版的 `backup`、`window` 字段。现行配置结构不使用它们，也没有相应的备份或窗口行为。配置写入会保留已有的未知字段，但不会让这些字段生效。

## 本地 HTTP API

首次使用空数据库时，先按[迁移说明](migrations/README.md)依次手动执行 `001_initial.up.sql`、`002_schedule_changes.up.sql`，再从仓库根目录运行 `go run ./cmd/flutter`。服务固定监听 `http://127.0.0.1:10086`，仅接受 `Host: 127.0.0.1:10086`，不启用浏览器 CORS 或应用令牌。启动前检查数据库版本和表结构；`GET /health` 每次复查。缺少迁移时服务不会监听。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/health` | 数据库及迁移健康检查 |
| POST | `/api/v1/scheduler/schedules` | 创建单次或周期日程；请求体最大 1 MiB |
| GET | `/api/v1/scheduler/schedules` | 查询未删除日程；支持 `name`、`type`、`limit`、`offset` |
| GET | `/api/v1/scheduler/schedules/:id` | 查看未删除日程 |
| PUT | `/api/v1/scheduler/schedules/:id` | 同类型事务更新、周期启停；必传 expectedRevision，支持 Idempotency-Key |
| DELETE | `/api/v1/scheduler/schedules/:id` | 软删除日程，成功返回 204 |
| GET | `/api/v1/scheduler/schedules?view=occurrences&include=unacknowledged&date=YYYY-MM-DD` | 同一读取快照的日期安排、待确认首分页及总数 |
| GET | `/api/v1/scheduler/schedules?view=occurrences&date=YYYY-MM-DD&cursor=...` | 日期事实与未来投影，分页去重 |
| GET | `/api/v1/scheduler/schedules?view=calendar&month=YYYY-MM` | 月份日期标记 |
| GET | `/api/v1/notifications?state=unacknowledged&cursor=...` | 全历史到期未确认页，含失败及待投递 |
| GET | `/api/v1/notifications/:id` | 提醒详情，父日程删除后历史仍可查 |
| POST | `/api/v1/notifications/acknowledgements` | 单条及批量统一提交 notificationIds，最多 500 个，整批校验后原子确认 |

创建请求示例：

```json
{"name":"工作日晨会","type":"period","description":null,"period":{"cron":["0 9 * * 1,2,3,4,5"]}}
```

单次日程使用 `"type":"single"` 和 `"single":{"notifyTime":"2026-10-10T09:30:00+08:00"}`；时间必须为分钟精度（秒与亚秒均为零）且晚于创建时刻。周期日程可传 `period.open` 原子创建暂停规则，缺省启用。两类规则不能同时出现，未知字段会被拒绝。成功响应使用 `{"data":{...}}`，创建返回 201 及 `Location`；列表返回 `{"data":[...]}`，按创建时间及 ID 降序排列。日程对象始终包含 `id`、`name`、`type`、`description`、`single`、`period`、`cancelled`、`createdAt`、`updatedAt`、`revision`，不适用的规则字段为 `null`。业务时间带 `+08:00`。

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

在 `desktop/` 运行视觉示例：

```powershell
flutter pub get
flutter run -d windows --dart-define=TIMECUE_DEMO=true
```

Widget 关系、状态所有权与验证命令见 [Flutter 工程说明](desktop/README.md)。CI 已替换为 Windows Flutter 格式、分析、测试与 Release 构建，保留 Go 检查。


分支与 Pull Request 流程、Go 与 Flutter CI 约定见[贡献规范](docs/CONTRIBUTING.md)。

阶段 C 的创建和更新可带 `Idempotency-Key`：相同键和内容返回原始结果，不同内容复用键返回 409；旧草稿的 expectedRevision 不匹配也返回 409。更新响应增加 effectiveAt，Go 在同一事务内冻结到期提醒的原名称/说明，保存版本边界、替换未来 pending 并计算新的首提醒。暂停和删除永久停止已有提醒尚未开始的投递，恢复严格从未来继续；已到期历史仍可查询、确认，不修改真实投递结果。
