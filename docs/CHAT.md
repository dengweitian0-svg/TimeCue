# 真实文字对话

正常、开发与发行入口统一使用真实聊天服务和基于官方 openai-go/v3 的 Chat Completions 适配器。首次运行不创建演示会话；没有模型配置或密钥时，应用正常启动，本地日程和历史查询仍可使用。

## 配置与运行

在当前实例 `--data-dir` 指定的数据目录中编辑 `config.yaml`，添加非密钥配置。例如：

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
  max_retry: 3
  first_timeout_seconds: 120
  idle_timeout_seconds: 60
```

`context_window` 是本地使用的上下文预算，应不大于所用模型的真实容量；`max_output_tokens` 缺省 8192，必须满足 `0 < max_output_tokens < context_window`。同一个 provider 可配置多个模型，同名模型通过 provider 区分。`base_url` 采用不带 `/chat/completions` 的 API 基址。

复制仓库 `example.env` 为同目录 `.env` 并填写密钥。密钥名由 `{PROVIDER}_{MODEL}` 转大写、非 ASCII 字母数字替换为下划线得到，例如 `DEEPSEEK_DEEPSEEK_V4_FLASH`。密钥从不进入 HTTP 响应、数据库请求记录或日志； `.env` 和模型配置的后续修改不需要重启。模型列表的 available 只表示本地配置及密钥存在，不代表上游认证成功。

打开 AI 页，选择提供方和模型后发送文字。密钥缺失时选项标为不可用；补齐后点击“刷新模型”。请求身份固定到本轮运行，重试相同幂等键始终返回原运行。

## 服务与存储

Go 启动时通过现有 PrepareSchema 初始化或备份升级数据库到版本 3；新增 `ai_sessions`、`ai_runs`、`ai_model_calls`、`ai_checkpoints`、`ai_events`、`ai_tool_calls` 六张表，原四张日程与提醒业务表保留。旧 Flutter 库升级前在原库旁生成一致性 `.before-upgrade-*.bak`，失败回滚；不转换 Wails 或未知结构。

调用模型前，短事务保存实际 input、running 和 checkpoint；流式增量满 5 条或首条等待 5 秒提交，后续增量不延后首条期限。结束、取消、错误和正常退出立即保存尾批，逐条保留原始事件，不合并或裁剪；尾批与相应调用结果和状态在同一事务保存，终态同时提交 checkpoint 和 error/end。只有成功提交的事件才供 SSE 和分页读取，事务失败不会留下已提交序号缺口。没有返回 usage 时保存 SQL NULL。会话软删除只隐藏父会话和公开子查询，不删除日程或执行事实。

HTTP 使用 `/v1/model`、`/v1/run`、`/v1/run/:run_id`、`/v1/run/:run_id/events`、`/v1/run/:run_id/event`、`/v1/run/:run_id/cancel` 与原有 session 路径。创建必填 session_id/input/provider/model，回执固定 run_id/status/output/error；概况固定十字段，错误与部分正文可以同时保留。thinking/text 仅含 content，error 含 code/message，end 仅含 status。事件页固定 events/next_after_seq/has_more；默认 10、最大 100 条，事件序号与 SSE 共用。

## 当前切片边界

T06 已接通只读日程工具。应用只装配一个统一 Registry；所有工具实现统一的 `Tool.Info(ctx)` / `Tool.InvokableRun(ctx, argumentsJSON)` 接口，并通过 `Registry.Register` 注册；注册中心统一提供模型定义、校验参数、分发调用和转换错误，拒绝重复名称和不支持的定义。具体日程工具通过正式业务窄端口查询，不读取日程表，不注册创建、修改或删除工具。

| 工具 | 查询能力 |
| --- | --- |
| `get_schedule` | 必填正整数 `id`，返回未删除日程完整规则和当前 `revision`。 |
| `list_schedules` | 可选 `name`/`type`/`limit`/`offset`，名称包含过滤；默认 50 条、最大 50 条，保留数组结果及每条规则版本。 |
| `query_occurrences` | 必填 `date`、可选 `cursor`，复用正式日期发生情况页和 `nextCursor`；支持 YYYY-MM-DD 及今天/明天/后天/昨天，实际解析时按北京时间计算。 |

模型请求携带相同预算内的真实工具定义；适配器按流式 index 组装名称、ID 和 arguments 原文。只有正常结束且持久化接纳的模型回复才推进工具，断流片段不执行或网络重试。同一回复内重复 tool_call_id 整批拒绝为 model_invalid_format，不登记工具或生成工具事件，原 output/usage 与已收到正文、思考仍保留；无法配对的 tool_calls 不进入下一轮有效上下文。不同回复允许复用同值 ID。带工具请求的正常 length 截断逐条校验：坏参数按原 ID 回填解析错误，同批完整查询仍执行；纯文字自动续写由后续切片扩展。

工具由单 run 协调器调度，当前只读查询最多三个并行；开始和结果事务串行合并。实际处理前先原子保存 running、arguments 原文的外层 JSON 字符串、调用关联、tool_start 与 tool_use checkpoint，提交后才做参数校验和正式查询，不创建 pending 占位。语法或参数不完整使用 tool_argument_parse_error；合法非对象、缺失/多余字段、类型或范围错误使用 tool_argument_validation_error，均不执行业务。开始事件 argument 对语法非法或非对象为 null，对可解析对象保持原对象。

每个工具的原始 JSON output/error、tool_result、原 ID 的 tool 消息、completed_tool、last_tool 和 checkpoint 同事务保存；成功与失败都回填模型，工具错误不再重复生成 run error 事件。全部原 ID 取得结果后才保存 tool_end 并登记下一普通模型请求。列表、对象、标量及 null 均保持原始 JSON 类型；Flutter 复用同一事件解码器、原 ID 配对和默认折叠详情，查看完整参数、结果及错误。

重启从已接纳回复和逐项 checkpoint 接续，不重发已成功模型请求、不重复已保存结果。中断且结果未确认的只读查询沿原记录使用剩余恢复预算，预算耗尽以 tool_result_unconfirmed 回填；不会按名称或事件猜测查询已完成。结果事务失败立即停止后续推进，以实际结果随失败收尾事务保存，仅重试持久化；其余已接纳但无确认结果的原 ID 在同一事务补齐 tool_result_unconfirmed，保留完整配对，接纳后立即取消也补齐 skipped/aborted。新的普通模型调用登记前读取最新重试及预算配置，已有调用的重试预算不变。当前工具参数和业务错误不自动重试；完整工具退避、配置化执行参数、写入取消与恢复由后续工具切片扩展。


T05 已接通完整消息快照接续和运行预算。新 run 从按创建时间选出的最新已结束运行 checkpoint.context 读取有效消息，追加本轮真实输入；不按 model_call.id 重新拼接，也不补回已被实际快照替代的历史。原始 run.input、事件、调用 input/output 均保持原文，客户端仍只提交本轮输入。快照保留实际消息顺序、后端续写请求、有效摘要投影及工具请求/结果的配对；这些历史格式已覆盖测试，写入工具、自动续写与摘要生成仍随后续切片交付。

失败或取消前已收到的 assistant 正文和 reasoning_content 原样参与下一轮；只有 thinking 也保留，完全无输出不构造空 assistant。Go 在旧轮所有消息及其配对结果之后、本轮真实输入之前追加独立 user 消息：“上一轮回复未完成；其操作表述不代表业务已经生效。”说明与实际 input 和请求前 checkpoint 一起保存，重启直接沿原 input 恢复，不重复追加；文字表述不作为业务成功证明。

整体预算使用实际 ModelRequest 完整 JSON 序列化结果，统一以 ceil(UTF-8 字节数 / 4) 估算；SDK 发送同一序列化内容，中文、emoji、转义和结构开销均参与计算。可用输入预算为 context_window - max_output_tokens，等于预算时允许请求。当前尚未实现自动摘要；超过实际预算时原子创建可查询、可幂等重放的 failed run，保存 context_budget_exceeded 和 start/error/end，不创建 model_call、checkpoint 或 compact，不请求上游或保留执行占用。首次创建的 HTTP 状态仍为 202，终态原回执重放为 200，JSON 始终只有 run_id/status/output/error。

chat.max_rounds 缺省或 0 使用 20，负数配置拒绝发布。普通轮数按同一 run 内 continuation=false 且不关联 compact 的逻辑 model_call 统计，失败摘要也不计普通轮数；网络重试和未完成调用恢复沿用原记录。达到上限且仍需下一次普通调用时以 model_round_limit_exceeded 失败，已有成功模型调用、实际结果、checkpoint 和部分正文保持原事实，不新增 run 配置快照或计数字段。

T04 已接通模型有限重试。每个逻辑 model_call 在登记时固定 max_retry，缺省首次外三次，显式 0 禁止网络重试；默认退避为 5/10/20 秒，后续按指数增加。retry_count 在计划一次重试时递增，retry_wait、next_retry_at 和 retry_history 原子保存；重试沿用原 model_call 和实际 input，不另建调用。retry_history 中每项保留 retry/at/result/cause/error，记录计划时间、结果和实际故障。退出保留计划期限，重启执行原计划不重复扣次；未完成 running 的生命周期重发仍不额外消耗网络预算。

只有本次及原调用尚无有效正文或思考时，超时、网络故障和明确的 429/500/503 可以重试；已收到部分内容后中断保留正文、思考、响应标识及实际 usage，明确失败。取消和会话删除会停止退避及新请求，最终只保存唯一 error/end，中间重试故障不伪造公开错误事件。退避期间删除 Key 或请求开始前取消，也会在终态事务中结算重试历史。模型未获工具定义时仍拒绝意外工具调用；已注册的 T06 查询工具按正常完整回复处理，同一流块伴随的正文、思考和 usage 保留。

每次实际请求前从固定实例数据目录读取最新 Key 和首次/空闲超时，本次请求期间固定；默认首次有效输出 120 秒、流式空闲 60 秒。keep-alive、空 delta、响应 ID 与 usage 都不延长有效输出期限。.env 的新增、修改、清空及删除立即影响后续读取；config.yaml 通过既有目录监听发布有效快照。模型身份保留原值，密钥名规范化冲突或负数参数报配置错误，重载失败保留旧有效配置。不探测密钥上游有效性，不自动复活旧终态 run。

上游 400/401/402/422 分别保存 model_invalid_format/model_auth_failed/model_insufficient_balance/model_invalid_params，不自动重试；429/500/503 分别为 model_rate_limited/model_server_error/model_server_busy。有效输出或传输超时为 model_timeout，断流或连接失败为 model_network_error；未分类 HTTP 响应以不可重试的 model_invalid_format 反馈。错误消息不回显上游正文或密钥。已接受任务的模型故障通过原 run/error 事件反馈，不直接成为本机 HTTP 错误状态。

创建时未配置的组合仍返回 400 invalid_request，已配置但本地不可用仍为 503 model_config_unavailable，均不创建运行或登记键；幂等回执及本地历史查询不受缺 Key 影响。界面将明确的配置拒绝视为创建失败并释放占用，显示检查本地配置的提示；补齐 Key 后点击“刷新模型”即可开始新一轮。其他结果不明的创建失败继续保留原键以供安全重放。

T03 固定创建幂等与并发准入：同一会话内只比较原始 input，同键同输入返回原 run，同键不同输入返回 idempotency_key_conflict；重放忽略新 provider/model 并保留原模型与终态 output/error。不同会话可使用同键，相同输入的新一轮使用新键。已删除会话先拒绝；重放先于新建配置、会话忙碌、全局名额及预算门禁。

创建用例在同一互斥区完成回读、准入和真实创建事务；缺少幂等键、空白必填字段、配置拒绝、会话忙碌及全局拒绝不登记新键，事务失败整体回滚。内部 running 与 wait_for_cancel 占用名额，同会话最多一个、全局最多三个；历史与 SSE 连接不占名额。启动 ready 前重建已有占用，正常终态持久化后只释放一次。持久化收尾重试耗尽仍沿父规格释放内存资源及名额，原 run 在当前进程不再推进、不伪造已落库终态，重启依据实际记录恢复。

新建与 running 重放返回 202；真正终态及取消收尾投影重放返回 200，均只含 run_id/status/output/error。取消收尾概况允许 cancelled 且 finished_at=null，仍可订阅、继续占用名额；启动恢复该状态只保存已有事实并提交取消终态，不重新调用模型。只读查询工具已接通；完整在途工具取消投影与写入恢复仍由后续工具切片交付。

持久化故障耗尽可能遗留超过三个未完成会话。启动登记全部原运行并保留幂等回执，协调器共用三个执行名额，多余运行等待推进；不丢弃记录、不伪造终态、不新增数据库队列。等待中的取消和退出只保存已有事实，不重新请求模型。

T02 提供完整原始运行记录、按需翻页与 SSE 断线接续。界面以 (run_id, seq) 合并两种来源，先补缺口、成功应用后再推进连续消费位置；分页 next_after_seq 不直接作为 SSE 消费位置。页面重建复用宿主持有的内容和游标，新 ViewModel 或进程从零读取所需历史。204 后停止订阅该运行，查询概况并补齐事件；后端没有保存客户端阅读或消费位置。

204 已确认运行结束，即使概况与历史同时读取失败也释放前端占用，显示“运行已结束，正在读取记录”并保留读取重试；具体成功/失败和最终正文仍等待真实概况，不重新建立 SSE。

消息中的“运行记录”按原始 seq 查看正文增量、思考、工具、错误及控制记录，思考与完整 JSON 默认折叠。run.output 单独作为最终正文投影，终态查询不会覆盖时间线，也不会把旧尝试或恢复重发的事件再次拼入最终正文。当前公开事件不提供模型调用、续写或恢复尝试分组信息，界面不猜测这些边界。

cancelled 且 finished_at=null 显示为取消收尾中，继续接收事件并保留运行占用；取消回执和独立 error 事件不表示已经结束。工具展示实例以开始事件 seq 与原工具 ID 区分，结果与尚未完成的同 ID 请求配对，支持查看参数、任意 JSON 类型的原始 output 及 error；后续回复复用同值 ID 时保留新实例。T06 查询工具已通过真实后端链路验证；完整等待工具取消仍随后续工具切片交付。

T01 提供真实文字模型请求、多轮原始历史、完成会话重启查询、无在途工具的模型取消及会话隐藏。正常退出关闭上游连接、保留未完成事实，下次启动沿同一逻辑调用与原 input 重发；保留此前已持久化的展示事件。

写入工具、完整工具取消与恢复、自动续写、摘要和上下文裁剪由后续切片扩展。当前向上游提供三个正式只读日程工具定义；不支持的完成原因如实失败。输入超预算保存可查询失败运行，不发请求。原始会话历史不会被展示事件或模型侧裁剪覆盖。

启动恢复只推进同一会话最新的未完成运行；已有后续运行的旧任务只失败收尾。恢复所选模型配置或密钥缺失时，先以 model_config_unavailable 收尾并保留已有部分正文，不将缺配置误记为预算错误。

未进入模型调用的预算拒绝输入仅保留在原始历史中，不加入后续有效模型上下文。恢复重发失败或再次退出且尚无新有效输出时保留原部分回复及响应信息；取得新正文或思考后用新响应替换。完整成功响应即使为空也保存本次响应和 usage，旧展示事件仍保留。

启动先取得监听端口，再启动聊天恢复；端口冲突不触发恢复模型请求。SSE 的 Last-Event-ID 必须为 0 或已持久化序号，非法游标返回 400 invalid_request，并保留已有订阅。事件分页 after_seq 仍允许超过当前末尾并返回空页。
