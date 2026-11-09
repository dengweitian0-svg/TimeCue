# 聊天配置与契约

TimeCue 使用真实 Chat Completions 服务和本地 SQLite，支持多会话、流式思考与正文、六个日程工具、协作式取消、摘要、续写和重启恢复。缺少模型配置或密钥时，日程和已有聊天历史仍可使用。空库从空会话开始。显式启用 `TIMECUE_DEMO` 时使用传统日程的内存示例。

[Agent 架构与流程图](archify/architecture-agent-20261108-172843/README.md)按模块展示固定源码版本的运行边界。

## 配置与运行

在桌面 AI 页选择「添加模型连接」或「管理模型」。DeepSeek 预设可改用自定义 Chat Completions 服务。每份连接有稳定的 `provider`、可编辑的显示名称和服务地址，可手填多个模型 ID，分别设置上下文与最大输出预算。同一个模型可以通过不同连接访问。已保存的 `provider` 固定，旧配置缺少显示名称时用原 `provider` 展示。

也可在实例数据目录编辑 `config.yaml`。应用默认使用程序目录，显式 `--data-dir` 可覆盖。托管模式下 Flutter 和 Go 使用同一目录。环境与启动命令见 [Windows 使用与更新](RELEASE.md)和[桌面开发说明](../desktop/README.md)。

```yaml
chat_model:
  - provider: deepseek
    base_url: https://api.deepseek.com
    models:
      - model: deepseek-v4-flash
        context_window: 32768
        max_output_tokens: 8192
chat:
  max_rounds: 20
  max_continue: 5
  max_retry: 3
  first_timeout_seconds: 120
  idle_timeout_seconds: 60
  tool_concurrency: 3
  tool_timeout_seconds: 30
  tool_max_retry: 3
  compact_threshold: 0.8
  tool_result_max_tokens: 8192
```

`base_url` 使用不带 `/chat/completions` 的 API 基址。上下文预算应符合模型实际容量，输出预算默认 8192，且必须大于 0、小于上下文预算。

将仓库的 [`.env.example`](../.env.example) 复制为实例数据目录中的 `.env`，再填写实际密钥。密钥名由 `{PROVIDER}_{MODEL}` 转成大写，非 ASCII 字母数字替换为下划线，例如 `DEEPSEEK_DEEPSEEK_V4_FLASH`。规范化后冲突的配置会被拒绝。密钥用于上游鉴权，HTTP 响应、日志和持久化请求均不携带密钥。模型列表只返回本地可用状态。

### 保存与热加载

- 新 Key 更新当前连接中全部目标模型。留空时逐模型保留目标身份的已有 Key，新增或改名的目标缺少 Key 则拒绝保存。移除模型时保留原密钥条目和历史。
- Flutter 串行更新同一目录下的 `config.yaml` 和 `.env`，保留其他连接、注释及未知字段。先替换密钥再发布配置，发布失败时尝试恢复密钥。重开界面仅显示各模型的本地密钥状态。读取或保存失败时可重试，保存失败保留草稿。
- 每次保存生成独立随机 `config_revision`。Go 随有效快照发布该标记。Flutter 最多等待十秒，核对目标提供方完整模型集合、各模型可用性及各自版本。待加载时可只重试加载，复用原保存结果。
- 已开始的上游请求沿用启动时的 Key 和请求预算。后续实际请求读取当前有效配置。配置校验失败保留上一快照，后续请求缺少原模型或 Key 时以 `model_config_unavailable` 收尾。当前会话、草稿和订阅可继续使用。

## 本机 HTTP 契约

| 操作 | 路由 | 响应或用途 |
| --- | --- | --- |
| 模型列表 | `GET /v1/model` | `models` 包含 `provider`、`model`、`available` 和可选的 `config_revision`。`available` 表示本地配置及密钥齐备。 |
| 创建运行 | `POST /v1/run` | 必填 `session_id`、`input`、`provider`、`model`，以及 `Idempotency-Key: create_session:<uuid>`。回执为 `run_id/status/output/error`。 |
| 运行概况 | `GET /v1/run/:run_id` | 返回原始输入、模型身份、状态、部分或最终正文、错误和时间。 |
| 事件分页 | `GET /v1/run/:run_id/events` | 以 `after_seq` 和 `limit` 分页。默认 10 条，最多 100 条，响应为 `events/next_after_seq/has_more`。 |
| 事件订阅 | `GET /v1/run/:run_id/event` | SSE，可用 `Last-Event-ID` 接续。已结束的运行返回 204。 |
| 取消运行 | `POST /v1/run/:run_id/cancel` | 受理后返回运行 ID 和 `cancelled` 投影。 |
| 会话列表 | `GET /v1/session` | 按最新运行时间返回可见会话 ID。 |
| 会话详情 | `GET /v1/session/:session_id` | 返回 `session_id/session_name/run_ids`。 |
| 删除会话 | `DELETE /v1/session/:session_id` | 同步隐藏会话，活动运行进入取消收尾。 |

同一会话内，幂等重放只比较原始输入，沿用原运行和模型身份。同键异文返回冲突。重放先于新建配置和准入检查，已删除会话优先拒绝。首次创建（含事务内直接保存的预算失败）和运行中重放返回 202，终态及取消收尾重放返回 200。同一会话最多一个活动运行，全局最多三个。`running` 和 `wait_for_cancel` 均占用名额。

真正的新建请求在模型组合未配置时返回 400 `invalid_request`，已配置但本地不可用时返回 503 `model_config_unavailable`。这两类拒绝不登记新幂等键、不占用名额。其他结果不明的创建失败可用原键安全重试。

事件以 `seq/type/data` 返回，分页和 SSE 共用已提交的连续序号。文本和思考的 `data` 只含 `content`，`error` 含 `code/message`，`end` 含 `status`。工具事件保留原配对身份及原始结果。无效 SSE 游标返回 400 并保留原订阅，分页游标超过末尾则返回空页。

## 展示与取消

Flutter 按 `(run_id, seq)` 串行合并分页和 SSE，先补缺口再前进。204 后用概况和分页补齐，停止订阅并释放本地占用。终态正文只补齐缺失文字，原始事件仍用于展示历史。

消息按事件顺序展示，相邻思考与工具共用可折叠区块，正文独立显示。实时正文和思考逐步显示，终态立即补齐已收到的内容，历史和断线补读直接显示。工具详情展示业务参数及实际结果，隐藏内部字段。相同工具 ID 可在不同模型回复中复用，界面通过开始事件序号区分实例。

取消先持久化 `wait_for_cancel`，再停止启动后续模型、工具和重试。公开概况的 `cancelled` 且 `finished_at=null` 表示仍在收尾，原 SSE 和占用继续保留。已经开始的工具等待实际结果或原期限，退避及未启动工具按原 ID 保存跳过结果。最终状态和唯一 `end` 提交后释放名额。

删除会话同时保存活动运行的取消意图，隐藏新查询并保留子记录和业务事实。Flutter 可直接删除已取得运行 ID 的活动会话。原连接继续跟踪收尾，断线后后续准入由服务端判定。

## 日程工具

| 工具 | 参数与业务入口 |
| --- | --- |
| `get_schedule` | 必填正整数 `id`，返回完整规则和当前版本。 |
| `list_schedules` | 可按 `name/type` 过滤，使用 `limit/offset`。默认和最大页长均为 50。 |
| `query_occurrences` | 必填 `date`，可选 `cursor`，返回发生情况和 `nextCursor`。 |
| `create_schedule` | 必填 `name/type`。单次要求 `notifyTime`，周期要求 `cron/open`。 |
| `update_schedule` | 必填 `id/expectedRevision`，提交至少一个修改字段，省略字段沿用原值。 |
| `delete_schedule` | 必填 `id/expectedRevision`，经正式事务软删除并处理联动提醒。 |

相对日期或时刻按实际解析时的北京时间计算，明确时间和已提交事实在恢复时直接复用。输入不完整或目标有歧义时先澄清。有依赖的操作分模型轮次，多项独立操作各自结算。

读工具可并行，写工具与其他有效尝试互斥。开始记录先持久化，再做参数校验和业务调用。原始结果、配对消息、事件和 checkpoint 同事务保存，整批配齐后继续模型。同一模型回复中重复的工具 ID 会使整批请求无效。工具参数和业务错误通过对应结果回填。

工具并发与超时可热读，调低并发保留在途尝试。每个逻辑工具首次登记固定重试预算，默认首次外三次。显式 0 只允许首次尝试。仅超时和明确暂时故障进入退避，默认 5、10、20 秒，迟到结果与后续尝试隔离。

删除首次登记后台执行键，业务事务先查回执再做版本检查。正常恢复先只读核对原回执，命中不扣重试预算。其余未确认工具沿原身份、参数及固定剩余预算重执行，耗尽时回填 `tool_result_unconfirmed`。无键创建可能再次生效，修改冲突按实际版本反馈。取消恢复仅复用已保存结果和原删除回执，未确认的在途调用记录未确认，退避或未启动项跳过。

## 请求预算、摘要与续写

新运行从最新已结束运行的有效消息快照接续。保留实际正文、思考及工具配对，失败或取消后的有效部分附独立中断说明。模型预算按实际序列化请求的 UTF-8 字节数除以 4 向上取整。可用输入预算为上下文预算减输出预算。无可压缩历史且超预算时原子保存失败运行和 `start/error/end`，输入只保留在原始历史。

有历史且达到 `compact_threshold`（默认 0.8）时可在创建前做一次完整摘要。`tool_result_max_tokens`（默认 8192）只约束模型侧文本，按 UTF-8 边界保留头尾和省略说明，原始结果与公开事件完整保存。工具批次之后仍超预算才触发运行内摘要。摘要使用[专用提示](../internal/agent/chat/compact.go)，保留普通提示和本轮真实输入，禁用工具。失败或取消时沿旧有效历史接续。成功切换和下一调用登记原子提交，仍超预算时保留有效摘要并记录失败。

正常 `finish_reason=length` 触发独立续写调用。普通轮数上限默认 20，普通和摘要续写共享默认 5 次续写预算，续写不计普通轮数。截断工具按原 ID 反馈解析错误，格式补全仅用于模型上下文，同批完整工具仍照常处理。运行的最终正文来自最后普通轮及其续写。

## 持久化、重试与恢复

SQLite 启动时初始化或升级到版本 3，原日程与提醒数据保留。可识别旧库升级前创建一致性备份，Wails 和未知结构拒绝自动转换。详见[数据库迁移说明](../migrations/README.md)。

模型实际请求在调用记录和 checkpoint 提交后发送。流式事件达到五条或首条等待五秒时保存，剩余尾批随退出、结果或终态事务提交。正常结束、取消与故障均以已提交记录为准。

模型重试沿用原逻辑调用和实际请求，登记时固定预算，默认首次外三次，0 表示禁用网络重试。只有尚无有效正文或思考的明确暂时故障可重试，默认退避 5、10、20 秒。每次实际请求读取当前 Key 和首次、空闲超时，默认分别为 120 秒和 60 秒。收到有效内容后中断会保留部分回复和实际用量。上游故障通过运行概况和事件反馈。

宿主先登记未完成运行及占用，再发布 ready。正常退出与 HTTP 共用五秒期限，保存实际已接收的内容和事件，未完成运行保留原进度。重启时已提交的完整响应直接复用，缺失或部分响应沿原请求重发，生命周期重发不另扣网络重试。已有后续运行的旧运行只做失败收尾。配置门禁只在确需请求模型时生效。

持久化故障立即停止推进并撤销同批有效工具尝试。已取得的结果及原调用身份随统一收尾保存，其余标为未确认。失败终态写入每隔五秒最多重试三次，只重试保存并保留占用。故障与重试记录 warning，耗尽时记录 error 并释放内存资源和 SSE。查询与重启以真实落库状态为准。

内部 `Runtime.GetRunUsage` 按需重算指定可见运行的原始用量，涵盖普通调用、摘要及续写。`prompt_tokens`、`completion_tokens` 和 `total_tokens` 分别合计。字段缺失、非法或溢出时保持未知，真实零值保留。`total_tokens` 只累加实际返回的总用量。原始扩展字段仍可读取，统计不推进运行或生成事件。

## 验证

本机可控上游回归覆盖配置热加载、聊天协议、恢复、工具、SQLite 及 Flutter 接入。HTTP 变更按[工程约束](../AGENTS.md#工程约束)先通过 Apifox。合成聊天夹具只用于 Widget 和截图测试。

真实模型验收从仓库根目录执行，按实例选择实际密钥文件和已配置模型。

```powershell
go build -o .gotmp-chat-real/timecue-core.exe ./cmd/flutter
python scripts/chat-real-smoke.py --go .gotmp-chat-real/timecue-core.exe --env=test --env-file .env --provider deepseek --model deepseek-v4-flash --base-url https://api.deepseek.com --flutter-test
```

脚本使用新建隔离目录、固定测试端口和真实上游，核验六个工具、实际日程与提醒、分页与 SSE、取消、模型和摘要恢复及重启后的终态查询。端口冲突时退出。未取得真实取消或恢复窗口时报告对应场景未覆盖。`--flutter-test` 通过正式网关只读核对这些记录。数据、日志、报告和密钥副本仅留本地。

发行目录和原生验证见[发行说明](RELEASE.md)及[贡献规范](CONTRIBUTING.md#ci-规则)。自动验证和真实 Windows 窗口、托盘及平台交互的人工验收分别记录。
