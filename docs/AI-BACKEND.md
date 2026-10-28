# AI 服务后端设计草案

本文记录讨论中已确认的业务范围和行为规则，不表示真实 AI 后端已实现。领域术语见 [CONTEXT.md](CONTEXT.md)。

会话历史保留原始用户输入、助手输出及工具处理事实；有效模型上下文是供模型使用的选取与压缩表达，由当前有效摘要及尚未覆盖的适用消息构成。摘要生效或模型侧内容裁剪不替换、删除原始会话历史，历史展示也不从摘要后的模型消息反推。

## 已确认范围

- 第一版提供聊天及日程读写工具，支持查询、创建、修改和单条删除日程。
- 日程工具通过现有 App 正式业务操作执行，日程与提醒仍由现有业务边界维护。
- 单条删除复用 App 软删除逻辑，并补齐期望版本检查与持久化幂等回执；不通过 Agent 直接删除业务记录。版本冲突按既定业务错误反馈模型，不自动重试。
- 批量日程操作通过多个单条工具调用完成，每条独立成功或失败，统一遵循正常工具调用的校验、调度、重试、幂等及结果回填流程；不因同批其他操作失败而回滚已成功的操作，不另设整批原子执行机制。

## 指令执行

- 用户发出明确、信息完整且目标唯一的日程操作指令后，助手可以直接执行，无须逐次二次确认。
- 信息不足或目标不唯一时，先向用户澄清，再执行对应操作。
- 执行后的反馈以实际业务操作结果为依据。

### 相对时间解析

- 相对时间由后端内部 parse time 函数解析，每次实际解析时使用当前北京时间（UTC+08:00），转换为明确时间后供日程业务使用。不将解析基准固定为 run.created_at，也不以对话创建时间代替实时时钟。
- 后续调用、工具重试或进程恢复确实需要再次进行时间解析时，函数使用该次调用时的当前时间；不因属于同一 run 而冻结日期。已经持久化的实际工具结果、明确时间及业务回执按既定恢复与幂等规则复用，不通过重新解析改写历史执行事实。
- parse time 属于内部时间处理能力，不通过改写用户原始 run.input 表达解析结果，不新增 run 级时间基准快照列。T07 的写入工具已支持 RFC3339、北京时间 YYYY-MM-DD HH:mm 和 今天/明天/后天/昨天 HH:mm，模型负责把“明天上午九点”规范成“明天 09:00”并保留相对词，后端在实际调用时解释。缺日期或时刻不补默认值，分钟精度及日期业务有效性沿正式入口校验；这不是任意自然语言解析器。

## 中断恢复

- 启动恢复先读取 run，按同会话后续 run 的存在性筛选业务恢复候选，只有仍允许恢复的 running run 才读取对应 checkpoint 并恢复执行；不先遍历 checkpoint 再推断哪些 run 应当运行。
- 某个 running run 之后已有同会话新 run 时，启动时将该旧 run 以 failed 收尾，不再恢复其模型或工具业务执行；后续 run 无论仍在运行还是已经终态，均使旧 running run 不再具备业务恢复资格。保留旧 run 已持久化的输出、工具结果及历史记录，不根据后续任务改写既有业务事实。失败收尾沿用既定终态事务及持久化故障重试规则。
- 每个 session 仅最新 run 可进入未完成任务恢复：最新 run 为 running 时按 checkpoint 恢复，为 wait_for_cancel 时只做既定只读取消收尾，已经终态则不恢复业务执行。没有后续 run 的旧 running run 仍按既定规则恢复；不将所有未完成 run 一律置为 failed。wait_for_cancel 的只读取消收尾规则保持不变。
- 没有 running run 时不启动正常业务执行恢复；wait_for_cancel 独立进入只读取消收尾分支。两类 run 都不存在时，不执行其他恢复操作。
- checkpoint 是执行恢复位置的唯一依据；run 状态决定是否进入恢复，checkpoint 决定从哪一步继续，不通过 event 推断执行进度。
- run 创建后允许暂时没有 ai_checkpoints 记录；checkpoint 为空表示首次模型调用尚未发生。仅当 run 为 running 时，按未开始首次模型调用的流程恢复；wait_for_cancel 和终态仍优先遵循其取消及禁止继续执行规则，不因 checkpoint 为空重新发起请求。
- 发起模型请求前，用一个短事务保存 model_call.input、running 状态，以及 checkpoint 的执行阶段和对应 model_call_id；普通调用使用 model_call_start，摘要及其续写使用 compact，事务提交后才发请求。收到完整回复后，再用短事务保存调用结果与对应进度；普通调用结束使用 model_call_end，摘要继续时保持 compact，完整成功且普通输入在预算内时原子切换下一次普通调用。结果中的工具调用尚未执行时，恢复后执行这些工具，再将结果放入后续模型请求。
- tool_start 在本批工具处理期间持续更新，每个工具执行完成后都保存一次 tool_start 及最新 state；全部工具处理完成后才保存 tool_end。恢复时已有结果的工具不重复执行，直接复用保存的结果。
- 工具执行中崩溃、尚无完成 checkpoint 时，只读工具可以重新读取；dangerous=true 的删除日程工具使用原幂等键及业务回执支持恢复。创建、修改日程不使用工具幂等键，结果未确认时在剩余重试预算内再次执行，复用原 tool_call 记录和 input，不新建逻辑调用、不补造幂等键；每次恢复重执行也增加 retry_count，与普通工具重试共用上限。
- 业务执行已提交、完成 checkpoint 尚未保存的间隙仍按结果未确认处理；有持久化幂等键时按原身份核对或恢复，无键的创建、修改按上述规则重新执行，仍遵循现有参数及版本校验。该选择允许创建操作重复生效，不按日程名称或时间猜测去重；取消恢复仍只读核对，不重新执行。
- 此处“回传模型”指将工具执行结果纳入后续模型请求的上下文。
- 正常退出或进程崩溃后，模型未完成请求的恢复重发沿用此前不额外增加 retry_count 的规则。工具恢复时，复用已保存结果或只读核对已有回执不增加次数；实际重新执行未确认的工具调用则增加 retry_count，不能绕过工具最大重试上限。已使用次数跨重启保留，不清零。
- 模型或工具发生可重试错误后，真正发起重试时才消耗一次重试机会。恢复到原有 retry_wait 后执行原计划的重试，仍按该次重试正常计数，不因恢复再多扣一次。
- 模型 max_retry=0 时仍允许从 checkpoint 恢复未完成请求；最终失败或取消的 run 不因恢复规则重新执行。

## 协作式取消

- 取消采用协作式流程，按收到 cancel 请求时的实际执行阶段处理；停止启动后续模型调用、工具和重试，接受请求本身不代表取消已完成。
- run 正在进行 model_call 且没有正在执行的工具时，关闭与模型的 HTTP 请求或流连接，结束本次模型调用；保存此前已经接收的正文、thinking、工具参数片段及其他实际响应内容，model_call.status=cancelled，run 进入 cancelled。不能因取消丢弃已经接收的内容，也不等待模型完整返回。
- 模型取消时，立即保存已接收但尚未刷新的事件及 model_call.output，保留已取得的模型回复 ID、实际 usage 或实际错误；没有取得的内容不虚构。取消后不继续接纳迟到响应，不据此执行新工具或继续正常循环。取消导致的连接关闭不进入重试，也不将 run 改为 failed。
- run 仍有工具正在执行时，先持久化 wait_for_cancel；已开始的每个工具等待本次执行完成或达到原有超时期限，不因用户取消立即中止正在执行的业务，也不重新计算完整超时。工具成功保存 output，失败或超时保存 error；均更新原 tool_call，保留真实状态与执行结果，不用统一 cancelled 状态替代工具结果。
- 并行工具须全部完成或超时、实际结果均已保存后，run 才从 wait_for_cancel 转为 cancelled。等待期间既有 SSE 保持订阅，暂不发送 end；只有 cancelled 持久化成功后才向前端发送唯一 end，其 data 为 {status: "cancelled"}，随后结束订阅。
- 没有正在进行的模型调用或工具时，可以在取消收尾后直接提交 cancelled；尚未发出的模型请求不再发送，尚未开始或处于 retry_wait 的工具不再启动，未再执行的调用标记 skipped，并在上下文中按原 ID 回填 content="aborted" 的 tool 消息。
- wait_for_cancel 尚未进入终态，继续占用活跃 run 名额；只有 cancelled 持久化后才释放名额。在等待期间，新消息仍以 session_busy 拒绝，不排队、不保存该次新建请求的幂等键。
- 内部 wait_for_cancel 继续在 HTTP 响应中投影为 cancelled，取消回执、run 查询及创建幂等回执均沿用这一对外状态，不暴露内部等待状态。此投影只影响展示，不能依据它提前发送 end、关闭订阅或释放名额；end 只在内部 run 真正 cancelled 且结果已持久化后发送。
- 取消完成事务原子保存 run.cancelled、finished_at、对应 checkpoint、最后待保存的模型内容或工具结果及唯一 end 记录，提交后才推送 end。工具收尾跨多个结果事务时，每次及时保存已完成的工具及进度，最后一个工具结算时提交取消终态；不持有事务等待 HTTP 或工具执行。
- 重复 cancel 复用已有取消请求及结果；wait_for_cancel 期间不生成 end，已 cancelled 后不重复生成 end。取消完成前断开 SSE 不影响后台收尾及终态保存；重连 wait_for_cancel 的 run 仍可订阅，直到收到终态 end，只有真正终态才返回 SSE 204。
- 取消收尾发生实际工具错误时，保存 tool_call.error，并由对应 tool_result 向前端传递，不再为同一工具错误生成独立 error 事件；模型或 run 自身的收尾错误仍保存 error 事件。实际执行结果、取消跳过结果及运行错误提交后照常按 seq 推送并在后续查询中返回，须先于最终取消 end。因取消而跳过的工具也保存 tool_result、分配 seq 并发送前端，不隐藏任何已记录的取消收尾结果。工具错误不将取消中的 run 改为 failed，run 仍按取消流程结束。
- 取消期间每份实际工具结果、错误、context 中的工具结果、completed_tool、last_tool 及 checkpoint 按既定短事务规则保存；取消跳过的工具保留原 assistant.tool_calls，并在上下文中回填原 ID 对应的 tool 消息，content 为字符串 "aborted"。取消收尾只保存这些消息，不再请求模型；后续新 run 可按既定历史规则使用它们。模型输出或工具结果的记录不依赖前端 SSE 是否仍连接。
- 等待取消期间，已开始工具产生的原始 output 仍按工具结果事务保存 tool_result 并推送；对应结果和错误事件须先于最终取消 end 保存及推送，不因本次 run 将被取消而丢弃已获得结果。
- run 生命周期状态与 checkpoint 执行位置分别记录；checkpoint 决定恢复位置，run 状态决定是否允许继续正常推进。checkpoint.status 不新增取消或终态枚举，run 的终态由 run.status 表达。
- 取消收尾期间进程崩溃，重启后只核对已经存在的工具结果或业务幂等回执，不再重新执行工具；已有结果或回执时补齐 tool_call，无法取得时记录结果未确认，不断言业务未生效，也不补做操作。核对只用只读查询，不能调用“没有回执就执行写入”的入口代替查询。
- 恢复 wait_for_cancel 后完成只读收尾，将 cancelled、checkpoint 及尚未生成的唯一 end 一并持久化；不能因存在 checkpoint 就恢复正常业务推进。排队工具继续忽略，模型适配端口须支持取消请求和释放流连接。

### 恢复实现约束

以下约束由上述恢复语义推导，具体表结构见后文列级草案，迁移 SQL 尚未实施：

- 执行工具前，必须已有持久化的调用标识、工具名称和完整参数；dangerous=true 的调用还须保存幂等键。恢复重试使用相同标识和参数，有幂等键时复用原键，不为 dangerous=false 的工具补造幂等键。
- 写入去重约束应标识同一次逻辑操作，不能以日程名称或提醒时间代替操作身份。
- 使用幂等键的日程业务写入与对应回执须在同一事务提交，保证业务已生效时能够找回原操作结果。现有创建、更新入口具备可选幂等能力，但这两个工具使用空业务 key，不保存对应回执；删除工具需补齐这一能力。
- 已保存到 checkpoint 的工具结果恢复时直接复用，包括只读工具结果。
- event 负责输出记录与消费，checkpoint 负责执行恢复；事件已落库不代表对应模型调用或工具操作已完成 checkpoint。

## 模型请求重试

- 模型尚未产生有效输出时，超时、网络中断、限流及服务暂不可用等可重试错误，允许在首次请求之外重试三次，等待间隔依次为 5 秒、10 秒、20 秒。
- 三次为未配置时的默认重试上限；用户自定义更高上限时，继续按第 n 次重试前等待 5 × 2^(n-1) 秒的指数退避规则计算。
- 鉴权失败、无效请求等明确不可重试错误，直接使 run 进入 `failed`。
- 上游返回认证失败时，按下文官方 401 类别映射为 model_auth_failed，不自动重试，将已创建的 run 结束为 failed；对应 model_call.error、run.error 和 error 事件使用同一 {code, message} 错误信息，message 说明实际认证问题，不包含 API Key。error 事件及失败终态按既定事务保存，提交后先推送 error 再推送唯一 end；不改为创建入口的 503 本地配置错误。
- 每个模型调用步骤独立计数，与工具调用重试预算分别管理；模型调用的已用重试次数、上限和退避进度保存在同一条 model_call 中，跨重启保留。
- 重试仍从 checkpoint 对应的上下文重新请求，使用新模型回复 id 记录新输出。
- 三次重试全部失败后，保存最终错误，当前 run 进入 `failed`，通过 SSE 报告错误并发送最终 `end` 事件，不继续停留在 running。
- 模型已经产生部分有效输出后报错或 SSE 超时，当前 run 直接进入 `failed` 并发送错误及最终 `end`，不再在原 run 中自动重试，等待用户创建新 run。
- Go 进程退出或崩溃后的恢复仍遵循 checkpoint 规则，与模型请求自身报错或超时分别处理。
- 用户已请求取消时，遵循协作式取消规则，不再发起模型重试。
- 其余具体错误码及模型重试预算的原子记账方式继续细化；失败终态统一由 end.data.status=failed 表达，具体错误通过此前的独立 error 事件提供，不再额外编码 SSE 结束原因。

### 上游模型错误分类

- 第一版上游 API 返回错误的类别直接采用 [DeepSeek 官方错误码文档](https://api-docs.deepseek.com/zh-cn/quick_start/error_codes/)，对外 error.code 沿用简短的小写下划线语义字符串。下表 HTTP 状态码仅供模型适配器判断错误类别，不直接作为 error.code，也不透传为本机已接受 run 的创建回执或后续查询请求的 HTTP 状态；语义 code 是本项目定义，不是上游的数字错误码。

| 上游判断依据 | 错误类别 | error.code | 本项目处理 |
|---|---|---|---|
| 400 | 请求结构不符合协议 | model_invalid_format | 不自动重试，run 失败。 |
| 401 | Key 认证未通过 | model_auth_failed | 不自动重试，run 失败。 |
| 402 | 账户可用余额不足 | model_insufficient_balance | 不自动重试，run 失败。 |
| 422 | 请求参数校验未通过 | model_invalid_params | 不自动重试，run 失败。 |
| 429 | 请求频率或并发受限 | model_rate_limited | 按既定有限重试规则处理。 |
| 500 | 上游内部服务故障 | model_server_error | 按既定有限重试规则处理。 |
| 503 | 上游负载繁忙 | model_server_busy | 按既定有限重试规则处理。 |

- 重试仍受既定有效输出、取消及预算规则约束；已有部分有效输出后发生错误时，不因上游类别允许重试而绕过原有失败规则。表中的处理是本项目策略，官方页面提供错误含义及处理建议，不替代项目的运行状态机。
- error 继续使用 {code, message}，code 为语义字符串，不改成数字或数字字符串，也不为这次分类新增对外结构字段。上游七类之外仅补充 model_timeout 和 model_network_error：首次有效输出超时、流式空闲超时使用 model_timeout，连接失败、连接中断等网络故障使用 model_network_error，具体原因由 message 说明，不伪造上游 HTTP 状态。不新增 model_invalid_response 专用 code。
- 超时与网络故障沿用既定有限重试及有效输出规则：没有有效输出时按预算重试，已有部分有效输出时保留内容并使 run 失败；用户取消和进程正常退出仍走各自流程，不归类为上述运行错误。

### 模型请求超时

- 使用两个独立、可配置的计时：首次有效输出超时，以及流式空闲超时。
- 首次有效输出超时从请求发出后开始计算，限制等待正文、思考或工具增量的最长时间。
- 流式空闲超时在开始有效输出后生效，限制连续没有有效增量的时间，收到有效增量时重新计时。
- keep-alive 注释不属于有效模型输出，不重置上述计时；正常结束信号仍按完整响应结束处理。
- [DeepSeek 请求保活说明](https://api-docs.deepseek.com/quick_start/rate_limit/) 允许等待推理时发送 keep-alive 注释，连接存活不等于已产生有效内容。
- 首次有效输出前超时沿用有限重试规则；已有有效输出后超时，保留已有输出并使当前 run 进入 failed。
- 首次有效输出超时默认 120 秒，流式空闲超时默认 60 秒；用户未配置时使用默认值，两项均允许用户配置。
- 每次实际发送模型 HTTP 请求前，一次读取当前有效的首次输出超时和流式空闲超时，本次请求期间保持不变；网络重试、进程恢复重发及 continuation 新请求均在各自发出前重新读取最新配置。不随 run 创建时冻结，不新增数据库超时快照或运行参数列；已开始请求不因配置热更新重置计时或修改超时值。model_call.max_retry 仍按既定规则在该逻辑调用登记时保存，不因这项超时读取规则改变重试预算。

### 输出长度限制与自动续写

- 正常对话的模型响应因长度限制结束时，保留输出并在同一个 run 内自动续写，不将长度限制本身作为 failed 的原因。
- 续写上下文保留三段结构：assistant:a 是模型已输出但被截断的内容；后端发现截断后追加续写请求 b；assistant:c 是下一次模型调用返回的内容。a、b、c 分别保留为独立消息，不能将 a 改成 user，也不能省略后端请求 b 后只串接两条 assistant。
- text 或 thinking 被截断时，b 使用 user 角色发送续写指令，例如“继续”或“继续未完成的输出”；tool argument 被截断时，b 使用 tool 角色，tool_call_id 沿用 a 中原调用 ID，content 为参数解析错误及重新输入完整工具调用的要求。这类工具续写不额外追加一条 user 续写请求；同批其他工具的结果仍须全部按原 ID 补齐后才请求模型。
- 后端生成的 b 同实际消息结构保存到 checkpoint.state.context 及下一次 model_call.input；它不是新的真实用户输入，不覆盖 run.input，也不计为新 run。a、c 保留各自实际 model_call.output，下一次调用以 continuation=true 标记续写。
- 下一次请求按普通模型调用流程创建新的 model_call，并设置布尔标记 continuation=true；非续写调用为 false。各次调用保留实际 input/output，run 最初的用户 input 不被合成的“继续”覆盖；continuation 是用途标记，不是关联外键。
- 该调用属于续写，不属于对同一请求的网络重试，不增加原 model_call 的 retry_count，也不消耗普通 Agent loop 轮次。
- 已正常收到长度结束信号的响应与网络异常导致的流中断分别处理；后者仍遵循已有输出后 failed、等待用户新建 run 的规则。
- 自动续写使用独立预算 max_continue，默认每个 run 5 次，该上限与其他运行参数一样纳入统一 config，由 default.yaml 提供默认值、config.yaml 提供用户配置；不单独加载或管理，不消耗普通 Agent loop 轮次。
- 已使用续写次数跨重启保留；网络重试及生命周期恢复不会额外消耗同一次逻辑续写的次数。
- 已用续写次数直接统计当前 run 中 continuation=true 的逻辑 model_call 记录，包括普通回复及摘要的续写；不按事件条数或网络请求次数累计，不另加 run 续写计数字段。
- 达到续写上限仍需继续时，保留已有输出，run 以明确的续写上限错误结束为 failed；不将此前正常收到的长度结束响应改写为模型请求失败。
- 摘要生成命中长度限制时也允许自动续写，与正常对话共用当前 run 的 max_continue 预算；每次续写创建新的 model_call，不消耗普通 Agent loop 轮次。
- 摘要使用 compact 事件；其输出需要续写时，新建普通 model_call 并设置 continuation=true，输入包含已有上下文与未完成的摘要输出，再追加内容为“继续未完成的输出”的 user 消息。续写本身不代表再次摘要，不因此新增 compact；不增加 purpose 枚举或 continuation_of 关联字段。
- 正常收到 finish_reason=length 时逐条判断工具调用：参数完整且通过 schema 校验的调用可以执行业务；被截断的调用也创建 tool_call，记录实际 input 及解析 error，最终为 failed，不执行业务、不进行工具重试。保留原 tool_call_id，通过对应 tool output 返回参数解析异常并要求重新输入。不能因一个工具被截断而禁止同一回复中的其他完整有效调用；仍须等模型响应正常结束后再接纳处理，网络异常中断不按此规则放行工具。
- 工具参数被截断时，tool_start event 对应函数项的 argument 保存 JSON null，保留 id、name，不保存截断参数片段、不新增 raw_argument；记录一次 warning 日志，并将参数解析异常及重新输出完整工具调用的要求放入原 tool_call_id 对应的 role=tool 消息。旧 ID 依然有效，用于配对这条错误结果，不再作废；不使用独立 user/assistant 普通文本替代 tool output。后续续写请求仍设置 continuation=true，遵循长度续写预算，不属于原请求的网络重试。
- 续写产生的 thinking、text、tool_start、tool_result 等事件按普通模型调用流程处理，同时按上一轮被截断的内容类型衔接：thinking 被截断时，将新一轮 thinking 接到上一轮 thinking 中，其他事件正常写入；text 被截断时，将新一轮 text 接到上一轮 text 中，其他事件正常写入；tool 被截断时，以 argument=null 的 tool_start 事件供前端展示，创建保存 input/error 的失败 tool_call，并通过原 ID 的 tool output 告知模型参数解析异常，不执行业务；后续使用本轮完整 tool，不拼接两轮 arguments。既有结束原因检查与工具 schema 校验规则继续生效。
- 参数修复不跨两次模型回复拼接 arguments；重新生成的调用按模型该次实际返回的 tool_call_id 和完整参数处理，重新通过结束原因及 schema 校验后才可执行。原调用 ID 继续用于对应其解析错误结果，不将后续新调用的业务结果回填为原错误调用的成功结果，也不强制增加调用 ID 唯一性约束。
- 只收到部分工具参数时，先对已接收片段做 JSON 格式补全，再将补全后的内容放入原 tool_call_id 对应的 role=tool 消息，明确声明“该工具参数不完整，未执行业务操作，请重新输出完整调用”，并保留既定解析错误信息。补全仅供模型理解已接收内容，不推测缺失的业务值，不将补全后的 JSON 作为可执行参数，也不因格式合法而视为完整调用。上下文仍保留原 assistant.tool_calls 的调用身份和配对关系，不以独立 user 消息替代该 tool 反馈。
- JSON 格式补全只形成后续模型上下文中的反馈内容，不覆盖原 model_call.output 或 tool_call.input，不将补全结果冒充 tool_call.output 的业务结果；tool_start.argument 对截断参数仍为 null，前端 tool_result 继续返回既定原始 output/error。生成的 tool 消息按实际内容保存于后续 model_call.input.messages 与 checkpoint.state.context，恢复时复用，不重复回填。
- 同一回复中工具 A 完整有效、工具 B 被截断时，A 按工具开始流程登记并执行，B 的 tool_start 事件保留 id、name 且 argument=null，不保存截断参数片段；model_call.output 仍按实际响应记录规则保存，B 的原标识保留。下一次模型请求前，先补齐 A 的实际 tool 结果及 B 原 ID 对应的参数解析错误 tool 结果，再请求模型重新输出 B，不要求重新生成已处理的 A。
- 发给模型的消息保留原 assistant.tool_calls 中 B 的调用身份，紧随其后的 role=tool 消息使用 B 原 tool_call_id 返回参数解析异常和重新输入要求，不从 assistant.tool_calls 中删除 B，不插入独立 user/assistant 消息替代该结果。API 消息按供应商协议保存原调用结构，不能把展示事件中的 argument=null 直接当作供应商 arguments 字段的序列化格式；event 仍仅以 id、name、argument=null 保留调用事实。
- 各次 model_call 保留实际 input/output；thinking/text 的续写增量分别新建 event 行，关联实际产生该增量的 model_call，保留新事件的 SSE 编号，不更新旧 event 行、旧编号或旧调用关联。读取展示时按既定被截断类型归并内容，模型上下文仍保留 assistant:a、user/tool:b、assistant:c 的独立消息结构，不因展示归并而合并消息或改变角色。截断 tool 的解析错误通过原 ID 对应的 tool message 进入下一次模型请求，不虚构业务执行结果、不拼接截断参数，后续完整调用按该次模型实际返回的标识处理。

## 中断后由用户继续对话

- 用户主动中断后，下次发送消息创建新的 run；新请求的模型上下文包含上一轮已有的部分输出及本次用户输入。
- 新 run 的上下文纳入上一 run 已终结完整工具调用的真实结果或校验错误，不把校验拒绝误报为业务已经执行。因取消而跳过的调用同样保留在原 assistant.tool_calls 中，并配对 role=tool、tool_call_id 为原 ID、content 为字符串 "aborted" 的消息；已产生实际结果或错误的调用仍保留原结果，不改写成 aborted。
- 模型已有部分有效输出后出现异常返回，或等待模型 SSE 连接超时，应向前端发送相应的 SSE 错误信息，并将原 run 置为 `failed`；用户重新发送消息时创建新 run，携带上一轮部分输出作为上下文，再接收新的回复。尚无有效输出的请求先遵循上述有限重试规则。
- 这不将旧的部分回复补记为完整回复，也不据旧的未完成工具参数执行工具；已有工具执行事实仍以 `tool_call` 及正式业务结果为依据。
- 历史上下文全部由 Go 后端组装，前端创建 run 时只提交 session id、本轮用户输入、provider 与 model，不上传完整历史 context。
- 后端根据 session 定位最新成功生效的 compact 及其有效摘要，并沿最新适用的上下文快照接续所有尚未被该摘要覆盖的消息，再追加本轮真实用户输入；不按 run 数限制历史，也不补回已被摘要覆盖的原文。所需用户输入、完整回复及已执行工具结果分别从 run、model_call、tool_call 补齐，仍遵循既定接纳及筛选规则。
- 新 run 接续失败或取消的上一轮时，保留已收到的部分正文原文，作为未完成的 assistant 回复纳入历史，并向模型明确说明上一轮已经中断、该回复未完成。日程操作是否生效以真实工具结果及正式业务结果为准，不能将“我已经创建……”等未完成表述视为操作成功证明；不改写原 model_call.output 或将旧调用补记为成功。
- 中断说明由后端生成独立的 role=user 消息，不放入 system prompt；与本轮真实用户输入分别保存，不覆盖 run.input，不因此创建额外 run。该消息按实际结构进入 model_call.input.messages 与 checkpoint.state.context；置于上一轮已有消息及配对工具结果之后、本轮真实用户输入之前，不插入 assistant.tool_calls 与其 tool 结果之间。恢复时复用已保存消息，不重复追加。
- 网络中断或取消后，新 run 纳入上一轮未完整接收的工具参数时，同样使用上述 JSON 格式补全及原 ID 对应的 role=tool 反馈，明确参数不完整且该请求未执行业务；不删去原调用后仅用 user 中断说明代替，不从片段恢复业务执行。该规则只确定后续上下文投影，不将网络中断改为同一 run 的自动续写，也不覆盖此前已完成工具的真实结果或既定取消跳过调用的 aborted 结果。
- 上一轮仅收到 thinking、尚无正文时，下一轮仍携带该部分输出，以 role=assistant、reasoning_content 为已收到的 thinking 原文、content 为字符串 "" 组成消息；同时收到 thinking 与正文时，分别写入同一 assistant 消息的 reasoning_content 与 content，不将 thinking 转成正文或 user 消息。字段名统一为 reasoning_content，不使用 reasonning_content。各字段按所属模型回复分别累积，仍保留既定 continuation 消息边界。
- 上述 assistant 消息进入实际 model_call.input.messages 与 checkpoint.state.context，恢复时完整复用 reasoning_content 和 content，不因正文为空删除已有 thinking。后端携带 reasoning_content 不等于供应商一定使用它；即使供应商在跨轮请求中忽略该字段，后端仍按此契约保留并传入。该规则不改变有效摘要只拼接摘要正文、不包含 thinking 的既定投影。
- 中断说明继续使用后端生成的独立 user 消息，其具体文案尚待细化。前端按既定事件顺序保留并追加已有可展示片段，不因进程恢复或上下文分界而删除旧展示内容。

## Run 运行预算与默认配置

- 每个 run 的正常模型调用轮数有上限；同一步模型请求的网络重试不额外计入轮次，摘要压缩与自动续写的模型调用也不消耗普通 Agent loop 轮次。创建前摘要和运行中因工具结果超预算而触发的 compact 均不计普通轮次，不另设 compact 次数预算；模型调用自身的重试限制及长度续写的 max_continue 规则仍分别适用。
- 最大普通轮数是统一 config 中的可配置项，default.yaml 提供默认值 20，config.yaml 提供用户配置；与其他配置一起加载、校验和更新，执行器使用配置模块统一发布的有效值，不为该参数单独建立读取或更新机制。
- 已使用的轮次跨进程重启保留，恢复不能重置运行预算。
- 普通已用轮次直接统计当前 run 中 continuation=false 且未关联 compact 事件的 model_call 记录；摘要身份按对应 compact 是否存在判断，不取决于 compact_succeeded，失败或未完成的摘要也不计为普通轮次。每条逻辑调用仅计一次，不因其产生多条事件、网络重试或恢复重发而重复计数，不新增 run 轮次计数字段。
- 达到上限且仍需继续时，run 以明确错误进入 `failed`，保留已经生效的业务操作，并按终态流程发送 `end`。
- 取消创建 run 时固定有效运行参数、重启恢复及幂等重放沿用该参数快照的规则；不新增 config_snapshot，也不将这些配置分别作为 run 的列保存。
- 最大普通轮数、续写上限、模型重试上限、摘要阈值、模型超时、上下文/输出预算及工具运行参数均纳入现有统一 config，由 default.yaml 提供默认值、config.yaml 提供用户配置，沿用正常配置的加载、合并、校验及更新机制。执行器使用统一发布的有效配置，不为普通轮数或续写上限另设一套按新调用读取配置的机制。工具定义及其元数据仍内置在代码中；工具参数默认并发 3、单次超时 30 秒、最多重试 3 次。已用轮次、续写和重试进度属于执行事实，按既定记录保留，不因配置管理归属调整而清零。
- 具体字段、参数有效范围和配置校验方式尚待确定。

## 数据库与数据职责

- AI 与日程、提醒共用当前 SQLite 数据库文件，AI 数据使用独立的数据表，不混入现有日程、提醒表。
- 聊天模块通过自己的窄存储接口访问 AI 数据，复用应用已有的数据库连接、迁移、备份与生命周期管理。
- 现有日程操作及业务幂等回执仍由原业务边界管理，AI 表不替代日程正式持久化。
- 模型请求、工具执行及退避等待期间不持有数据库事务；事件与执行进度使用有界的短事务保存。
- 当前需要持久化的逻辑数据包括 session、run、model_call、checkpoint、event 和 tool_call；物理表名已确定为 ai_ 前缀的复数形式，完整字段、约束与迁移版本继续按本文细化。
- 共用文件及独立表的选择仅为已确认设计；尚未修改数据库结构或运行迁移。

### 持久化故障

- 工具已经返回实际结果，但保存结果与 checkpoint 的事务失败时，停止该 run 的后续推进，以 run.status=failed 收尾，并记录 error 级日志；failed 终态成功落库后释放活跃名额，若失败收尾写入持续失败则按下文三次重试耗尽规则释放。不新增 paused 状态，不因本次保存失败重新执行业务工具，也不发起下一次模型调用或新的工具步骤。
- 同一 run 仍有其他工具在途时，持久化故障路径直接退出当前运行，不等待这些工具完成或超时，不把收齐整批工具结果作为 failed 提交及名额释放的前提；释放遵循失败终态提交成功或三次收尾重试耗尽的规则。已经获得的结果按失败收尾规则处理，迟到回调不能重新推进该 run、覆盖终态或重复生成 end；不把未等到结果的业务操作断言为未生效。此规则不改变用户取消时等待在途工具完成或超时的既定流程。
- 当前进程保留已经取得的实际结果及原调用身份用于失败收尾，run.error 反映持久化故障，不将工具已经成功执行的事实改写为工具业务失败。原有已持久化内容保留；尚未落库的结果不提前通过 SSE 推送，日志也不替代执行事实的正式持久化。
- 持久化故障日志包含 run_id、适用的 model_call_id 与工具调用标识、失败的保存阶段及原始存储错误，重试日志同时包含本次重试序号；使用现有日志设施，不依赖同一次失败的数据库事务。failed 终态及 error/end 的保存与推送沿用既定终态事务规则；成功落库的 failed run 不在重启后继续执行。
- 若 failed 终态自身的首次保存失败，每隔 5 秒重试失败收尾事务，首次尝试之外最多重试 3 次；每次重试记录 warning 日志。仅重试保存已取得的结果及失败终态，不重新执行模型或工具，不消耗模型或工具重试预算。重试期间保留会话及全局活跃名额，当前运行保持停止推进；任一次提交成功即停止重试，只结算一次名额并推送已持久化的 error/end。
- 三次重试全部失败后记录 error 日志，停止本次收尾重试并释放该 run 的运行资源、会话及全局活跃名额，允许后续请求按正常准入规则创建新 run；原 run 在当前进程不再自动推进或接受迟到回调。此为持久化故障耗尽时的名额释放例外，不要求失败终态已落库，也不将释放内存运行资源视为数据库状态已改为 failed，不推送未落库的终态事件。对外持久化查询仍以已保存记录为准；进程退出仍遵循已有 5 秒退出预算，重启仍按数据库实际状态及 checkpoint 恢复，不将日志当作 checkpoint 或业务回执。

## 模型调用记录与 Run 边界

- 一个 run 表示从用户输入到模型最终输出的完整处理过程；失败或用户取消同样终结该 run。
- run 的业务字段包括 run_id、session_id、input、status、output、error；input 仅保存本轮真实用户输入，output 保存最后一轮面向用户的完整正文，其中包含该轮续写内容，不汇总此前独立模型轮次的回复。正文先输出 A、截断后续写 B 时，run.output 保存 A+B，各次 model_call.output 仍分别保存实际输出。error 仅在实际发生时记录，无错误时为空。
- run、model_call、tool_call 的 error 字段统一保存 {code, message} JSON 对象，列声明使用 JSON，允许 SQL NULL；无错误时保存 SQL NULL，不使用空字符串、空对象或 JSON null 代替。三张表均添加 CHECK (error IS NULL OR json_valid(error))，在数据库层拒绝非空的非法 JSON；对象字段结构由应用层保证。普通 SQLite 表的 JSON 类型声明本身不自动校验格式，类型及函数语义参见 [SQLite 类型规则](https://www.sqlite.org/datatype3.html) 和 [SQLite JSON 函数](https://www.sqlite.org/json1.html)。
- thinking、text、tool_start、tool_result 的完整明细由 event 表保存，不向 run 增加 items 或完整事件列表字段；各次模型调用的实际请求与结构化响应仍由 model_call 单独记录。
- Agent 在同一个 run 内可以多次调用模型，并在模型调用之间执行工具；一次模型请求不等同于一次 run。
- 使用独立的 `model_call` 表记录模型调用的 input 与 output，并关联所属 run 和 session。
- 模型未响应或请求失败时，仍记录已发生的模型调用行为及对应 error，不能只保留成功获得回复的调用。
- 未取得响应时 output 可以为空，模型回复 id 可以为空；调用记录本身不能依赖供应商已返回回复 id 才存在。
- model_call 使用数据库自增主键 `id` 作为本地关联依据，不额外生成一套 model_call_id 业务标识；供应商返回的模型回复 id 单独保存，允许为空。
- 模型调用相关事件通过 event.model_call_id 关联 model_call.id；不在 model_call 增加指向 event 的外键。event.model_call_id 是本地调用记录的引用列，不是新增的模型调用业务标识，也不使用供应商回复 id 代替。
- 一条 `model_call` 对应一次逻辑模型调用；首次请求和三次重试共用同一条记录，不因重试新增 model_call。
- 首次请求和三次重试均未响应时，仍保留这一条 model_call 及其最终 error，当前 run 进入 `failed`。
- 每次模型请求重试时，更新同一条 model_call 的重试记录；重试次数及退避进度属于该逻辑调用，必须跨进程重启保留。
- 达到重试上限后，在 model_call 中保存最终 error，按终态原子提交流程完成 run，再通过 SSE 向前端返回具体错误信息。
- 重试记录保存已使用次数、最大次数、下次可重试时间及每次重试的时间、结果和错误原因；明细以 JSON 数组保存在同一条 model_call 内，不因重试新增调用行。
- 同一 run 中，工具结果回传后发起的下一次正常模型调用创建新的 model_call；模型调用轮数按逻辑步骤计数，重试不增加轮数。
- 模型调用记录为后端上下文组装提供持久化输入输出；event 继续负责最详细的增量输出记录和回放。
- 回复是否完整接纳由 `model_call` 自身的完成状态统一拥有，取消此前规划的内部 `model_reply_completed` 事件。
- model_call.input、model_call.output 的列声明统一使用 JSON，分别保存实际请求与组装后的响应；error 独立保存。input 使用 json_valid 检查，允许为空的 output 使用 CHECK (output IS NULL OR json_valid(output))。
- input 保存实际模型请求，包括完整上下文、模型、工具定义及生成参数。
- output 保存解析组装后的回复，包括正文、思考、工具调用、API 原始 usage 和结束原因等，允许只有部分内容或为空；usage 仅作为后端统计来源，不生成 event 或 SSE，聚合统计交由独立任务处理。
- ai_model_calls 另设 usage JSON 可空列，提取保存模型实际返回的原始 usage，作为统计读取入口；未返回时为 SQL NULL，不写零值或 token 估算值。添加 CHECK (usage IS NULL OR json_valid(usage))，与调用 output、状态及 checkpoint 在既定结果事务中保存；失败或取消时也保留已实际取得的 usage。output 中原有响应内容保持原样，不因独立列而改写供应商响应。
- HTTP 的 run.output 为最后一轮面向用户的正文及其续写内容合并后的投影，不汇总整个 run 的其他独立轮次历史文本，也不直接暴露 model_call 的完整结构化输出；完整展示从 event 明细取得。续写聚合不改写各次 model_call.output 的原始输出。
- model_call 需要区分正常对话、摘要压缩与自动续写用途；各类调用同样保留 input、output 及错误记录，摘要与续写调用不计入普通 Agent loop 的模型调用轮数。
- model_call.continuation 仅标记是否为续写调用；续写仍走普通模型调用流程，其事件通过 event.model_call_id 关联本次调用，不借续写将旧事件的模型调用关联改为新调用。
- 回复、续写及摘要各段的顺序统一以 event 顺序为准；model_call.id 仅作关联依据，不按其大小或 model_call 行的相邻关系推导内容顺序及续写分组。按事件顺序定位相关调用，再读取其 continuation 标记及实际输入输出。
- 关联表使用 model_call_id 引用 model_call 的自增主键；字段类型及空值规则见后文列级草案，模型适配边界仍需细化。

### 模型调用状态与原子提交

- 每次新逻辑模型调用先在一个短事务中写入 model_call 及实际 input，将调用状态设为 running，并保存按当前配置取得的本次 max_retry；同时创建或覆盖该 run 的 checkpoint：普通调用 status=model_call_start，摘要及其续写 status=compact，model_call_id 引用本次 model_call.id，state 按既定结构保存。事务提交成功后才实际发出模型请求，事务失败则不发请求。
- 完整回复收到后，在另一个短事务中保存该调用的 output、模型回复 id、原始 usage 等剩余结果、调用完成状态及 checkpoint；普通调用结束使用 model_call_end，摘要流程继续时保持 compact，state 的对应变更同事务保存。摘要完整成功并切换普通调用时采用下文的原子切换：同事务完成摘要结果和下一次普通调用登记，最终 checkpoint 指向新的普通调用并为 model_call_start。失败、取消及重试仍遵循已有原子提交与预算规则；网络请求、流式等待均不跨越数据库事务。
- model_call_start 表示调用已经登记并进入 running 阶段，不能据此断言 HTTP 请求已经实际发出；崩溃恢复通过 checkpoint.model_call_id 精确读取原调用及 input，沿用原逻辑调用、重试记录和预算。

| 状态 | 含义 |
|---|---|
| `running` | 调用已登记，正在发起请求或接收模型输出；首次持久化即为该状态。 |
| `retry_wait` | 尚无有效输出，等待重试。 |
| `succeeded` | 可接纳的模型响应已完整接收并保存；不表示用户最终回复已结束，可以需要续写或继续执行工具。 |
| `failed` | 最终失败，错误已保存。 |
| `cancelled` | 因用户取消而结束本次调用。 |

- model_call.status 仅保留 running、retry_wait、succeeded、failed、cancelled，不使用 pending；工具调用同样取消 pending 状态。
- `succeeded`、`failed`、`cancelled` 三种状态均须将状态、已获得的 output、error 及对应 checkpoint 变更放在同一个事务中提交。
- `failed`、`cancelled` 允许保留部分 output；未取得输出时可以为空，完整与否由模型调用状态明确表达。
- failed 可能发生在模型返回之前，也可能发生在接收过程中；有 output 时保存已有输出，没有 output 时留空，不虚构空回复。调用记录、失败状态和 error 均须保留。
- `succeeded` 仅表示该逻辑模型调用完整完成，不表示整个 run 已结束。
- 整个 run 成功、失败或取消结束时，run.status 表达终态，对应 checkpoint 保存最后的执行状态及快照，不额外进入 run_end 状态；模型调用成功但仍需执行工具时，不据此结束 run。

### 模型重试字段

| 字段 | 含义 |
|---|---|
| `retry_count` | 已使用的重试次数，首次请求不计入。 |
| `max_retry` | 本次逻辑调用开始时按当前配置保存的最大重试次数，未设置时默认 3，不含首次请求；0 表示不重试，同次调用的重试及恢复沿用该值。 |
| `next_retry_at` | 下次可重试时间，供重启恢复退避进度使用。 |
| `retry_history` | JSON 数组，列声明为 JSON 并通过 json_valid 检查，保存每次重试的时间、结果和错误原因。 |
| `error` | 最终调用错误；重试明细中的临时错误不等同于最终失败。 |

- `retry_count` 达到 `max_retry` 且仍失败时，保存最终 error 并结束为 failed，通过 SSE 返回具体错误信息。
- 以上字段不改变“已有部分有效输出后报错不再自动重试”的规则。
- model_call.max_retry 在本次逻辑调用开始时按当前配置保存，此后直到该调用结束均沿用该值，包括网络重试及重启恢复；不因配置变化修改正在进行的逻辑调用上限。新的 model_call 再读取最新配置，不从 run 参数快照取得，retry_count 仍按实际重试累计、不清零。
- max_retry 为 0 时，首次请求失败后直接按不可继续重试的终态流程保存错误，不产生重试。

## Checkpoint 状态快照

- checkpoint 只保存恢复运行所需的最小快照，不复制全部详细事件或保留历史快照链。
- 同一 run 的当前 checkpoint 每次更新时覆盖上一份内容。
- ai_checkpoints 以 run_id TEXT NOT NULL PRIMARY KEY 关联 ai_runs.run_id，不另设自增 id，保证每个业务 run 最多一条当前 checkpoint；该值使用业务 run_id 字符串，父列 ai_runs.run_id 使用 UNIQUE 保证唯一。
- checkpoint.status 为独立状态列，允许 compact、model_call_start、model_call_end、tool_start、tool_end 五种值；compact 表示摘要流程，包括摘要续写，普通调用使用 model_call_start/model_call_end，工具阶段使用 tool_start/tool_end。不使用原 model_call、run_start、run_end，也不增加 compact_end；run 的生命周期由 run.status 表达。
- checkpoint.model_call_id 使用本地模型调用主键，引用 ai_model_calls.id；与对应调用的 input、running 及 checkpoint 执行阶段在同一短事务中保存，普通调用为 model_call_start，摘要及其续写为 compact，不依赖供应商回复 id。
- checkpoint.state 记录执行快照，结构固定为 {context: array, completed_tool: array, last_tool: str}；state 列声明为 JSON，并使用 json_valid 检查。model_call_id 为独立关联列，不加入或扩展这三个 state 字段。
- state.context 保存当前完整的 message 数组，包含 system、tool、assistant、user 消息及各自必要字段；按既定摘要及截断处理规则形成有效上下文后完整保存，不仅保存消息引用或本轮增量。重启后的 Agent loop 先以 checkpoint.model_call_id 定位 model_call，读取 input.messages 并加载本次 output，不以回放全部事件重建基础历史；state.context 仍保存工具阶段回填进度。input 继续保存请求前的实际输入，本次 output 单独加载并按原角色、用途及 continuation 规则接入，不能为恢复方便回写 input 或重复追加消息。
- state.completed_tool 仅保存当前一次 model_call 已完成结果回填的 tool_call_id 字符串数组，不复制结果、不累计其他 model_call 的标识。成功、最终失败、参数解析或校验失败的结果回填后加入；取消跳过且已回填 content="aborted" 的 skipped 调用同样加入，表示该调用已经得到对应的 tool 消息，不表示业务执行成功。尚未开始的请求以及 running、retry_wait 调用均不加入，避免恢复时跳过仍待处理的调用。开始新的 model_call 时，在保存新调用及其 checkpoint 阶段的同一事务中将列表置为 []，并将 last_tool 重置为 ""；普通调用使用 model_call_start，摘要及其续写保持 compact。恢复同一次调用或重试其中工具时不清空其他工具已有完成进度，完整 context 保留。
- state.last_tool 为字符串，首次调用尚无工具记录时初始化为 ""，每次开始新的 model_call 时同样重置为 ""；每次实际进入工具调用步骤时写入 "tool_use: tool_name-tool_id"，不在模型返回工具调用消息时一次性预写所有工具。工具结果作为 tool message 写入 context 时，同步记录 "tool_result: tool_name-tool_id"。tool_id 沿用领域术语 tool_call_id 的含义；该字段只描述当前 model_call 最近一次工具动作，不能代替 completed_tool 表达整批完成情况。
- 创建 run 时不强制同时创建 checkpoint；首次模型调用登记前允许 ai_checkpoints 中没有对应行，这就是 checkpoint 为空，表示首次模型调用尚未发生。无需创建占位行或额外初始化状态；发起请求前先原子保存调用 input、running 及相应 checkpoint 阶段，普通调用为 model_call_start、摘要为 compact，保证请求不会在 checkpoint 尚未持久化时发出。
- 工具阶段通过 state 保存恢复所需的完整消息上下文、已完成工具标识及最后工具标识；每次工具调用时更新 last_tool 的 tool_use 标记。已回填的工具结果作为 tool 消息保存在 state.context 中，与对应 tool_call 结果、本次 completed_tool 更新、last_tool 的 tool_result 标记及 checkpoint 进度在同一事务中保存；checkpoint 内不另设工具结果字段。tool_call 独立保存执行详情与结果。
- tool_start 是可持续覆盖更新的批次进度，不是整批仅写一次的开始标记。例如当前 model_call 要求执行 A、B：A 完成后写 tool_start，completed_tool 包含 A 的 tool_call_id；B 完成后再写 tool_start，列表包含 A、B 的标识；全部工具完成后才写 tool_end。并行工具的每份结果均须由 run 协调器合并后写入，不能互相覆盖已完成进度。
- 恢复时通过 checkpoint.status=compact 识别摘要流程，model_call_id 指向当前摘要调用或摘要续写调用；调用运行、等待重试或完成的状态从 model_call.status、output 和原始 finish_reason 判断。摘要续写登记新 model_call 时更新关联，checkpoint.status 仍为 compact；不依赖新 state 字段识别用途。完整摘要成功时，在同一事务中保存摘要结果并登记下一普通调用，checkpoint 直接切换到该普通调用的 model_call_start；摘要失败或取消时保留 compact 与执行快照，run 终态优先决定不再继续执行。

| checkpoint.status | 含义与恢复边界 |
|---|---|
| `compact` | 处于摘要或摘要续写流程；关联当前实际 model_call，由该调用状态、output 和 finish_reason 判定请求恢复、等待重试或继续摘要。完整成功后原子切换普通调用的 model_call_start。 |
| `model_call_start` | 调用 input、running 与 model_call_id 已在同一短事务持久化；实际模型请求在事务提交后发出，恢复时沿用关联调用。 |
| `model_call_end` | 模型调用已结束，调用结果与相应执行快照按既定原子提交规则保存。 |
| `tool_start` | 本批工具处理阶段，每个工具完成后持续更新 state 并覆盖保存一次该状态。 |
| `tool_end` | 本批工具处理完成，直接使用 state.context 中完整消息数组继续模型调用。 |

重启恢复先从 checkpoint.model_call_id 读取关联 model_call.input.messages 及本次 output；output 缺失时先进入既定错误恢复流程，结合 run、model_call 状态和 checkpoint 阶段判断未发出、调用中断或结果尚未保存等情况，不能直接追加新用户消息进入正常流程。compact 沿用摘要流程，结合关联调用的状态和 finish_reason 执行原调用恢复或摘要续写，不当成普通回复结束 run；model_call_start 沿用原普通逻辑调用，tool_start/tool_end 按既定工具进度恢复，已回填结果仍完整保存在 state.context。output 已存在也仍须按状态和阶段完成未结束的步骤。running run 无 checkpoint 时仍按首次模型调用尚未发生处理。completed_tool 始终仅对应当前 model_call；last_tool 初始为空字符串，实际调用时写 tool_use，结果 message 持久化时写 tool_result。

### 已确认的 checkpoint 列

| 列 | 类型 | 约束及内容 |
|---|---|---|
| run_id | TEXT | NOT NULL PRIMARY KEY，关联具有 UNIQUE 约束的 ai_runs.run_id，每个 run 最多一行，不独立生成该值。首次模型调用前允许无行。 |
| model_call_id | INTEGER | NOT NULL，引用当前 run 的 ai_model_calls.id，与调用开始记录原子保存。 |
| status | TEXT | NOT NULL，CHECK 限定为 compact、model_call_start、model_call_end、tool_start、tool_end。 |
| state | JSON | NOT NULL，CHECK (json_valid(state))；包含完整 context、当前调用 completed_tool 及 last_tool。 |

开始新 model_call 的事务同时更新关联、status、state：context 保留当前完整消息，completed_tool=[]，last_tool=""。恢复同一次调用不按新调用初始化，不清空已完成工具进度。

### 重启后的两条读取路径

- Agent loop 的基础上下文：定位当前 session 最新适用的 checkpoint，通过 model_call_id 读取对应 model_call.input.messages，同时加载本次 model_call.output。input 仍是实际完整请求 JSON，取其中 messages，不将工具定义或生成参数直接当消息；output 中的回复按既定角色和接纳规则加入恢复消息，不把整个 API 响应 JSON 当成一条消息。已完成摘要及其 user 角色投影若已进入该 input，就随快照读取，无须重新从 compact 开始寻找全部摘要及续写调用。
- output 缺失时，先执行既定错误恢复流程，再开始正常流程；SQL NULL 不说明请求一定未发出，须结合状态和 checkpoint 判断。output 非空但仅有部分内容时，同样遵循已有失败、取消、完整回复接纳与 continuation 规则，不据非空值直接重发模型或执行工具；正文为空但包含 thinking 或 tool_calls 的真实响应不等于没有 output。
- 补齐本次 output 后，已回填工具结果从 state.context 按原 tool_call_id 复用，未完成工具按既定恢复流程推进；已存在的消息不重复追加。恢复正常后，后续新 run 沿该 messages 按既定摘要检查和上下文流程追加本次用户消息。continuation 继续沿用已有 assistant:a、user/tool:b、assistant:c 规则，不因重启另建续写协议；摘要回复仍按摘要生成阶段与生效后 user 投影的不同用途处理。
- 完整 session 展示：回放 ai_events，跨 run 按既定 run 创建时间、run 内按 seq 组织，保留各事件的展示与控制含义。用户输入沿现有 run.input 补齐，当前事件结构没有单独的用户输入事件；不将摘要投影后的模型 messages 当成完整原始会话展示。
- 运行恢复仍由 run.status、checkpoint.status 与既定工具进度共同决定，事件回放本身不发起模型或工具执行。终态 run 不重新执行，wait_for_cancel 继续只读收尾。
- 重启恢复推进到需要再次请求模型时，检查原 run 的 provider/model 配置及对应 API Key；缺失则将该 run 结束为 failed，error.code=model_config_unavailable，message 说明缺失的配置或密钥项但不包含密钥值。保留已保存的模型输出、usage、工具结果及恢复进度，不等待配置修复，不切换其他模型，不发 HTTP 请求，也不消耗网络重试次数。按既定终态事务保存实际需要结束的模型调用、run 错误、checkpoint 及 error/end 事件，提交后释放占用；此前已经完成的模型调用与工具结果保持原事实。修复配置后由用户通过新 run 继续，原 failed run 不自动复活。该检查只在恢复需要发出模型请求时生效，不将仅复用已有结果或 wait_for_cancel 的只读收尾改为配置失败。

### Run 终态提交

- succeeded、failed 的 run 终态、已有 checkpoint 的最终执行快照及最终 SSE end 记录在同一事务提交；失败时对应 error 事件也在该事务保存，按 seq 先 error 后 end，提交成功后再发送。checkpoint.status 保留五种执行阶段之一，不另设 run_end。创建前超预算且无可压缩历史时，在同一事务直接创建 failed run 和 start/error/end，不生成尚未发生的模型调用或 checkpoint。
- cancelled 的终态与对应 checkpoint、最后待保存的模型内容或工具结果及唯一取消 end 在同一短事务提交；wait_for_cancel 受理事务只记录等待取消，不生成 end。提交成功后才发送 end，不增加 checkpoint 终态枚举。
- 有本次需要结束的 model_call 时，其终态、输出和错误也在该事务中提交；结束 run 不改写此前已经成功的模型调用事实。
- 成功、失败和取消的 run 均不在重启后重新执行；进入 `wait_for_cancel` 尚不属于终态。
- 对外 SSE end 的 data 只保留 status，取值为 succeeded、failed、cancelled；删除 stop_reason 和 error。模型或 run 自身的错误通过独立 error 事件传递，工具错误通过原调用对应的 tool_result 传递；供应商原始 finish_reason 仍保存在 model_call.output，用于内部结束原因判断，不复制为 run 的结束原因，也不要求前端据此推断终态。
- 用户取消的 end.status 为 cancelled，发送时数据库中的 run 已为 cancelled，工具已完成或超时且结果已保存；wait_for_cancel 不提前发送 end。error 事件本身不表示 run 终态，终态仍由 end 及 run.status 表达。

## 工具调用记录

- 规划使用独立的 tool_call 表保存每次工具调用的详细信息，业务字段包括 name、input、output、error、status、run_id、session_id、tool_call_id、idempotency_key，另保留既定重试与恢复所需信息；尚未实施数据库结构变更。
- event 保存工具的基本信息，包括名称和输入，供输出记录及展示使用。
- tool_call 保存工具执行事实及结果；checkpoint.state.context 同时保存已经回填的完整 tool 消息，提供工具阶段的消息与进度快照。重启后 Agent loop 加载 checkpoint 关联 model_call.input.messages 及 output，按既定工具进度复用已回填结果、恢复未完成步骤，不从全部事件拼装工具执行状态，也不重复追加同批结果。
- 用户所称 `tool_id` 指模型返回的工具调用标识，文档统一称 `tool_call_id`，以区别于工具名称及模型回复 id。
- 一条 `tool_call` 记录对应模型提出的一次逻辑工具调用；同名工具的不同调用分别记录，崩溃恢复重试沿用原调用记录和幂等身份。
- `tool_call_id` 的配对作用域是同一次模型回复，不是跨 model_call 的全局身份。完整响应接纳前须检查该回复内全部工具调用标识；同一回复出现重复 ID 时，整份回复不予接纳，不执行其中任何工具，也不创建这批工具的执行记录或开始/结果事件。保留实际 model_call.output、usage 及已接收正文和思考，model_call 与 run 按 model_invalid_format 失败收尾，不自动重试、不伪造上游 HTTP 状态；即使重复项参数相同，也不能猜测合并。该检查先于逐条参数校验、取消跳过登记和正常 length 的部分工具放行规则；后续上下文不纳入这批无法配对的 tool_calls，也不伪造 tool 结果，已有正文和思考仍按部分回复规则保留。
- 不同 model_call 可返回相同 tool_call_id，分别属于各自调用，不按原 ID 全局去重；恢复时结合当前 model_call 定位本地 tool_call 记录，completed_tool 仅用于该 model_call。此接纳规则由服务保证，不增加 tool_call_id 或 (model_call_id, tool_call_id) 数据库唯一约束。
- 工具调用取消 pending 状态，不再预先批量创建等待执行的 tool_call 行。模型正常结束后，工具请求及参数原文保留在 model_call.output 和对应上下文中，调度器按并发和串行约束选取可开始的请求；开始处理时直接登记为 running，保存名称、实际收到的参数原文 input、模型调用关联、tool_call_id、适用的幂等键及从当前配置取得的 max_retry。参数被截断的请求也进入此流程，不以参数完整或校验成功作为创建记录的前提；参数解析与 schema 校验仍在 running/tool_use 登记后、工具入口内进行。重试及恢复复用原记录、身份及已保存的重试上限。
- 工具已经登记 running/tool_use 后，参数解析或 schema 校验失败即从 running 转为 failed，保存原始 input 及实际错误；这算已执行但失败的工具调用，不执行业务操作、不进行工具重试。dangerous=true 时保留开始登记时已保存的幂等键；dangerous=false 时该字段仍为 SQL NULL。按原 tool_call_id 将错误作为 tool message 写入 state.context，并加入当前 completed_tool，不另外新建一条失败调用。
- 每个工具首次开始时，用一个短事务创建 running 的 tool_call，保存 input、name、tool_call_id、model_call_id、适用的幂等键、retry_count=0 及本次 max_retry；input 将 arguments 原文编码为 JSON 字符串。该事务同时记录 tool_start 事件，并保存 checkpoint.status=tool_start 及 state.last_tool="tool_use: tool_name-tool_id"，保留当前完整 context 及其他工具已有完成进度。事务提交成功后进入工具执行入口，先解析与校验参数，通过后才调用业务函数；事务失败则不进入工具执行，校验及业务执行期间均不持有该事务。不再设置独立的批量 pending 登记事务。tool_start 表示工具执行开始，不在模型仅提出请求或请求仍等待调度时提前生成。
- 被截断的工具调用也创建 tool_call，input 按现有 JSON 字符串编码规则保存实际收到的参数原文，error 保存解析异常，最终状态为 failed；event 仍只保留 id、name、argument=null，不另存截断片段或 raw_argument。此处保留原文的字段是 tool_call.input，不将其复制给前端事件。原 tool_call_id 有效，按该 ID 返回错误 tool output，说明参数解析异常、需要重新输入，不执行业务且不重试该错误调用。
- 解析失败的 tool_call 状态、input/error、context 中原 ID 对应的错误 tool message、completed_tool 中新增的原 ID、last_tool 的 tool_result 标记及 checkpoint 进度，在同一结果事务中保存；恢复时复用已记录错误结果，不重复解析或重复回填。直接沿用 running/tool_use、failed 的处理流程，不将失败结果另建为第二次调用；本批全部完成后才进入 tool_end。
- 工具定义的 dangerous 元数据决定是否使用业务幂等键：仅 dangerous=true 的逻辑调用生成并保存 tool_call.idempotency_key，参数被截断或解析、校验失败也保留该键，格式沿用 tool:{tool_call_id}:{uuid}；tool 是固定业务标识，tool_call_id 使用模型返回值，uuid 由后端为该逻辑调用生成。该列为可空 TEXT，默认 SQL NULL；dangerous=false 时保持 NULL，不使用 none 或空字符串哨兵值。
- 当前仅删除日程工具定义为 dangerous=true；创建、修改及查询日程均为 false，不因其属于写操作而自动改为 true。dangerous、并行/串行标记等定义元数据由 tool 模块在代码中维护，不由外部工具配置文件或模型参数决定。
- dangerous=true 的调用仅生成一次 UUID 和完整幂等键，与首次工具记录一并保存，重试、重启恢复及业务回执查询均复用；参数校验失败也算该工具已执行，失败记录同样保留该键。危险标记不是按校验是否成功临时推断的。
- ai_tool_calls 使用普通 UNIQUE (run_id, idempotency_key)，保证同一 run 中非空幂等键不重复；不添加 tool_call_id 或 (model_call_id, tool_call_id) 的唯一约束。非危险工具使用 NULL，SQLite 唯一约束允许同一 run 保留多条 NULL 键记录，无需部分索引，参见 [SQLite 唯一索引](https://www.sqlite.org/lang_createindex.html#unique_indexes)。
- 工具结果回传模型时，必须使用对应的 `tool_call_id`，保持调用与输出一一对应；不能只按工具名称关联结果。
- tool_call 通过 TEXT 业务 run_id、session_id 关联所属 run、session，通过 model_call_id 关联 ai_model_calls.id；自身使用自增 id 主键。tool_call_id 不加唯一约束，也不与 model_call_id 组成联合唯一约束；执行恢复及回填按本地记录和已持久化的幂等身份定位，不能仅凭 tool_call_id 假设数据库记录唯一。
- `args` 与 `input` 指同一份工具调用参数，统一命名为 `input`，不维护重复参数字段。
- tool_call.input 列声明为 JSON，添加 CHECK (json_valid(input))；将模型返回的 arguments 原文作为一个 JSON 字符串编码后保存，不先解析 arguments 再重新序列化。读取时先解码外层 JSON 字符串，还原参数原文，再在内存中解析并进行 schema 校验；即使参数原文本身不是合法 JSON，也不丢失或改写原文。转换后的参数用于调用业务函数，不覆盖原始 input；执行结果保存到 output，实际错误保存到 error。

### 工具参数校验与业务执行

- 模型响应正常结束后，已取得名称及 ID 的工具请求按调度约束等待开始，包括参数被截断的请求；实际开始时直接登记 running/tool_use，在工具执行入口内解析并进行 schema 校验，只有参数完整且校验通过才执行业务操作。恢复后的尚未开始请求同样先登记执行，再解析和校验参数。
- finish_reason 为 length 且响应正常结束时，逐条执行完整性与参数校验；完整且通过校验的工具允许执行，仅截断的工具要求重新生成。正常结束信号不代表所有工具参数均有效，仍须逐条检查；不能在响应尚未结束或网络异常中断时提前执行工具。
- finish_reason 表示工具调用、长度限制或其他可接纳的正常结束原因，只代表可以继续逐条检查，不代表参数有效；不得直接据此执行工具。
- 后端根据工具名称定位对应执行入口；参数解析及 schema 校验属于该工具处理的一部分，不以校验成功为创建 running 记录的前提。被截断的调用同样登记，在解析阶段保存失败结果，不进入后续业务操作。
- 参数 JSON 已解析为对象但未通过 schema 校验时，不执行业务，将原 tool_call 从 running 更新为 failed，error.code 使用 tool_argument_validation_error，error.message 说明具体字段问题，保留原 input 及幂等键值。事件 argument 保留解析所得的参数对象，不因 schema 校验失败改为 null；对应错误 tool message、completed_tool、last_tool 的 tool_result 与 checkpoint 进度按既定结果事务规则一同保存。纳入下一次模型请求的每个调用都须有原 tool_call_id 对应的 tool message；此类错误不进入工具执行重试。取消跳过的调用保留原请求，并以原 ID 配对 content="aborted" 的 tool 消息。

### 工具调用状态

| 状态 | 含义 |
|---|---|
| `running` | 已登记 tool_use，正在工具入口内进行参数校验或后续业务操作。 |
| `retry_wait` | 本次执行失败，等待退避后重试，尚无最终结果。 |
| `succeeded` | 执行成功，结果已保存。 |
| `failed` | 不再重试，最终错误已保存。 |
| `skipped` | 因 run 取消而未再执行。 |

- 尚未开始就因取消而跳过的工具请求，直接创建 status=skipped 的 tool_call，保存原 input、调用关联及跳过原因，不经过 running，不执行参数校验或业务操作；同时记录并发送 tool_start 与 tool_result，两条事件均独立分配连续 seq，tool_start.seq < tool_result.seq，沿用同一原 tool_call_id。该取消路径是 tool_start 的明确例外，开始事件不构成实际执行证明，不伪造 tool_use；若调用此前已有 tool_start（例如 retry_wait 时取消），则复用已有开始事件，不重复新增。skipped 记录、所需开始/结果事件、context 中 content="aborted" 的原 ID tool 消息、completed_tool、last_tool 的 tool_result 标记及 checkpoint/取消进度在同一短事务保存，提交后按 seq 推送。
- 临时失败进入 `retry_wait`，不提前记录为最终 `failed`。
- 取消时已开始的调用继续保存实际成功或失败结果；未开始的调用标记 `skipped`。
- 因取消而跳过的已登记工具，标记 skipped 时同时保存对应 tool_result 事件并分配正常 run 内 seq，保留原 tool_call_id，结果 data 仍为 {tool_call_id, output, error}；output 保留已保存原值，没有则为 null，error.code=tool_skipped，message 说明实际取消跳过原因。该事件提交后通过 SSE 发送前端，历史查询及回放同样返回，不另发 error 事件。模型上下文保留原 assistant.tool_calls，并补齐原 ID 对应的 role=tool、content="aborted" 消息；该消息、completed_tool 中新增的原 ID、last_tool="tool_result: tool_name-tool_id"、工具 skipped 状态、结果事件及 checkpoint/取消进度在同一短事务保存。这里的 aborted 是模型消息的 content；不为未启动的工具伪造 tool_use 或业务执行事实，工具仍为 skipped。已有实际结果不得改写为 aborted，等待重试被取消时保留此前执行事实并停止后续尝试。
- 取消恢复中无法核实的中断调用，其错误须明确表达结果未确认，不能据此断言业务没有生效。

### 工具错误与重试

- 工具参数因截断或 JSON 语法错误而无法解析时，tool_call.error 使用下述固定结构；tool output 传递同一解析错误及重新输入要求，使用原 tool_call_id 配对。input 保留实际参数原文的 JSON 字符串编码，调用最终 failed，不执行业务且不做工具重试；该错误与中断后无法确认业务结果的 tool_result_unconfirmed 分别使用。

```json
{
  "code": "tool_argument_parse_error",
  "message": "工具参数解析异常，请重新输入完整的工具调用。"
}
```

- 参数能解析为 JSON object 但不符合工具 schema 时，error 使用 {code: "tool_argument_validation_error", message: "具体字段校验错误"}；message 根据实际缺失必填字段、字段类型不符等问题生成，不使用统一的解析错误文案代替。event.argument 保留已解析的参数对象，tool_call.input 保留原文，状态 failed。错误按原 tool_call_id 作为 tool output 写入 context，并与 completed_tool、last_tool 及 checkpoint 原子保存，不执行业务、不自动重试；模型修正后提出的调用仍按新的逻辑调用正常处理。
- arguments 为合法 JSON，但顶层是数组、字符串、数字、布尔值或 null，无法满足工具要求的对象类型时，同样属于 schema 校验失败，使用 tool_argument_validation_error，message 说明参数应为对象及实际类型；事件 argument 保存 JSON null，实际参数原文仍在 tool_call.input 中保存。该调用记录 failed，按原 ID 回填错误并加入 completed_tool，不执行业务、不自动重试；不能将这种类型不符误报为 JSON 语法解析失败。
- 日程不存在、版本冲突等明确的业务错误不自动重试，保存实际错误并按原 tool_call_id 反馈模型，由模型重新查询、调整操作或向用户说明。
- 工具错误默认不可重试：只有已确认的执行超时和明确识别为可重试的暂时性故障使用工具重试预算。尚未分类或不能明确判断为暂时性故障的错误直接将当前 tool_call 记为 failed，保存实际 error，并按原 tool_call_id 回填工具结果；不因仍有重试预算而再次执行。该工具失败不直接使 run 失败，补齐同批结果后由模型决定后续操作。进程中断后结果未确认的恢复重执行仍按既定独立规则处理，取消期间不启动重试。
- 工具单次执行超时统一使用现有 config.yaml 中的全局运行参数，未配置时默认 30 秒。每次实际执行尝试开始前读取当前有效超时，本次尝试期间固定；错误重试和恢复重执行在各自启动前重新读取最新值，不改变原 tool_call.max_retry 或 retry_count。用户取消不重置在途工具的原超时期限，配置更新也不改变其已启动的计时。不在各工具 schema 单独设置，不新增数据库工具超时快照或 run 参数列。
- 工具执行超时后立即取消当前执行，不等待工具实际返回；协调器按超时错误结算当前尝试、保存重试进度并释放执行名额，按既定退避规则安排重试。run 已等待取消时不安排新重试，继续取消收尾。
- 每个尝试只能被协调器结算一次；超时后的迟到返回不能覆盖当前状态、结束后续尝试或再次推进 Agent loop。各次重试仍沿用同一逻辑调用和持久化幂等键。
- 工具实现必须响应传入的 context 取消，并向内部数据库或网络操作传递该上下文；[Go context 官方契约](https://pkg.go.dev/context#CancelFunc) 中取消函数只发出停止信号、不等待实际工作退出，不能将发出取消等同于业务副作用已被回滚。写操作通过正式业务事务执行；只有携带幂等键的操作使用业务回执去重，无键创建、修改的重复执行仍按前述恢复规则处理。
- 工具超时，以及工具之外的故障导致的可重试执行错误，提供三次重试机会；按首次执行之外再重试三次理解，最多执行四次。
- ai_tool_calls 增加 INTEGER NOT NULL 的 max_retry，与 model_call 一样在创建本次逻辑调用时从当前有效 config.yaml 配置读取并保存，未配置时使用 3，不含首次执行。此后本调用的错误重试及恢复重执行均沿用该值，配置变化只影响新创建的工具调用，不改写既有记录；retry_count 仍按实际重试消耗累计并跨重启保留。
- 工具 max_retry=0 时允许尚未开始的请求首次执行，也允许复用已保存结果或只读查询已有幂等回执；不再发起错误重试或结果未确认后的恢复重执行。已保存次数达到上限且仍无法取得结果时，按既定最终失败及 tool_result_unconfirmed 规则结算，不因重启重置预算。
- 三次重试前的等待时间依次为 5 秒、10 秒、20 秒，采用指数退避。
- 重试沿用同一次逻辑工具调用的标识、参数和幂等身份，不创建新的逻辑调用。
- 重试耗尽后，保存最终错误，按原 `tool_call_id` 回传模型。
- 因应用中断未保存工具结果、且重试预算耗尽后仍无法确认结果时，tool_call.status 保存 failed，error 固定使用下述 tool_result_unconfirmed。该状态表示本次工具调用无法取得确定结果，不表示业务操作一定没有生效；错误按原 tool_call_id 作为工具结果写入 context，并与 completed_tool、last_tool 及 checkpoint 更新原子保存。尚有重试机会时继续按预算执行，不提前写入该最终失败错误；已取得实际业务错误时保存实际错误，不用此错误替代。

```json
{
  "code": "tool_result_unconfirmed",
  "message": "工具执行期间应用中断，执行结果未保存，且重试次数已耗尽，无法确认操作是否生效。"
}
```

- 重试期间尚未得到最终工具结果；执行成功或确定不再重试的错误，才形成可供后续模型请求使用的完成结果。
- 临时失败进入 retry_wait 时保存本次尝试及重试进度，checkpoint 继续保持 tool_start，不向 completed_tool 加入该标识；临时错误不作为最终 tool message 追加到 context。成功或最终失败时，将结果 message、completed_tool 中的标识、last_tool 的 tool_result 及相应 tool_call 状态原子保存；因取消不再重试时，改按 skipped 与 content="aborted" 的结果回填规则收尾。
- tool_call 持久化 retry_count、max_retry 及 next_retry_at，进程重启后按该行的已用次数、固定上限和退避时间恢复，不按重启时的新配置重建本次逻辑调用的重试预算。
- 恢复仍以 checkpoint 为入口，再读取关联 tool_call 的重试进度；退避时间尚未到则继续等待，已到则在剩余预算内恢复为可调度状态，不记录或恢复重入队位置。
- 真正发起错误重试或恢复重执行时均增加 retry_count，两者共用同一工具重试预算；恢复到 retry_wait 后执行原计划重试只计一次，不重复扣减。增加计数与 running、checkpoint.tool_start 及 tool_use 在执行前的同一短事务保存，提交后才执行；进程再次退出不会清零已消耗的次数。未知错误默认不可重试的规则已确定，其余暂时性故障的具体分类及错误码继续细化。
- 首次执行不计 retry_count。默认最多三次重试时，retry_count 从 0 开始，每次重试前检查是否还有预算，有则加一并执行；第三次重试仍失败或结果未确认且无法取得已有结果时，保存最终 failed 及实际错误，不发起第四次重试。不能仅因刚增加到上限就跳过最后一次允许的执行，也不能在该次已成功时改判失败。参数校验失败和明确不可重试的业务错误仍按既定规则直接 failed。
- 超时后不等待迟到成功结果重新裁定当前尝试；有幂等键时，已提交的业务结果由原键对应的业务回执保留，后续重试可复用。无键工具仍按已确认的再次执行规则处理。

### 工具调度与并行

- 工具定义（schema）标记该工具可并行或必须串行，执行器按该标记调度。
- 不约定工具 FIFO、启动先后或恢复后的队列顺序，不新增重入队 tool_start 事件或 queue_seq 字段。调度仅受调用状态、重试就绪时间、并发上限及并行/串行标记约束。
- 可并行工具在有可用名额时允许执行区间重叠；必须串行的工具仅在没有其他工具执行时启动，其执行期间不启动其他工具。不再通过队首或队列位置定义串行屏障。
- 每个逻辑调用仍独立保留调用身份、结果、错误和重试预算；本批全部调用产生最终结果后，进入 `tool_end` 并将结果按调用身份关联回模型。
- 每个 run 同时执行的工具数量上限可配置，默认值为 3；达到上限时，其余调用继续等待可用名额，并遵循串行工具不重叠的约束。
- 工具调度读取当前有效的并发上限，配置更新影响后续启动资格；调低上限不取消在途工具，等待正在执行的数量降到新上限以下后再启动；调高上限时可立即调度更多已就绪工具，仍遵循串行互斥、重试就绪及取消状态约束。不通过修改已有工具状态伪造名额释放，也不额外持久化 run 并发上限快照。
- 工具执行上限与最多 3 个活跃 session 的限制分别管理。
- 工具进入 retry_wait 后释放并发执行名额；到达下次重试时间后，原逻辑调用重新具备调度资格，沿用原调用身份、参数和幂等键，不保存队列位置、不生成新的 tool_start 事件。
- 退避中的工具不阻挡后续调用，包括后续串行工具；超时即取消并释放名额，不等待底层退出。串行与并行规则约束调度器当前有效的执行尝试，工具实现负责及时响应取消，业务幂等保证同一逻辑调用不会重复生效。
- 只要本批仍有 `retry_wait` 或排队中的调用，就不进入 `tool_end`；等待本批所有调用取得最终结果后才继续请求模型。
- 同一模型回复中的工具调用必须允许重试导致的执行重排，不能依赖同批其他调用先成功或先产生结果。
- 有先后依赖的操作分到不同模型轮次，取得前一步结果后，再由模型生成下一步工具调用。
- 可并行或必须串行标记只约束执行区间能否重叠，不表达业务依赖。
- 正常恢复时核对崩溃前处于 running 的工具调用：已保存结果直接复用，有幂等键的未确认调用按原身份恢复；无键的创建、修改调用结果未确认时，复用原记录和 input，在剩余重试预算内再次执行并增加 retry_count。预算已耗尽且仍无法确认结果时，保存工具最终 failed 和结果未确认的错误，按原 tool_call_id 回填模型；不声称此前业务操作未生效。尚未创建 tool_call 的请求从已保存的模型输出及上下文中恢复，首次执行不增加重试次数，按代码内置工具定义及并发/串行约束继续，不要求恢复原队列顺序。此规则不适用于 wait_for_cancel 的只读取消收尾。
- 不持久化调度队列次序，不使用 tool_call 主键或 event.seq 充当工具调度顺序；event.seq 仍负责 run 内事件展示与内容顺序。
- retry_wait 沿用保存的重试时间及剩余预算，到期后重新具备调度资格；wait_for_cancel 仍优先走只读核对及取消收尾，不重新执行工具。

### 工具结果回填与状态所有者

- 工具首次开始时，其 input、running、关联、幂等键、预算、tool_start 事件及对应 checkpoint 在同一短事务提交；恢复到 model_call_end 时从模型输出准备尚未开始的请求，恢复到 tool_start 时复用已有工具记录及完成进度，并补回尚未开始的请求，不重复创建已登记调用或重新生成其幂等键。
- 每个工具的 running 状态与对应 tool_use 恢复标记同事务提交，提交后才实际执行；工具完成后再以结果事务回填 context 中的 tool message、completed_tool 和 tool_result 标记，原 input 不被结果覆盖。
- 工具业务操作通过 App 正式入口执行，业务变更与幂等回执在业务事务内提交。
- 工具返回后，tool_call 的状态、output/error 与 checkpoint 的最新进度在另一个短事务中原子保存；两步之间的崩溃通过原调用身份及业务幂等回执处理。
- 每个工具完成时都更新一次 tool_start checkpoint，持续累积 state 中的本批进度；全部工具完成后再写 tool_end，不能等整批结束后才首次保存已完成结果及进度。
- 每个 run 由一个协调器持有执行进度；并行工具将结果交给该协调器，依次合并状态并提交，避免使用旧快照互相覆盖 checkpoint。
- 协调器串行处理状态变更与短事务，模型请求、工具执行和退避等待仍在各自运行边界内进行，不因串行提交而占用长事务。
- 本批最后一个工具取得最终结果时进入 tool_end；wait_for_cancel 的 run 优先完成取消收尾，不再进入下一次模型调用。

## 事件记录与回放

- 规划使用事件表保存前端展示与回放所需的 start、thinking、text、tool_start、tool_result、error、end、compact 共八种事件，普通对话、工具调用及结果、错误事件内容完整保留；原 tool 事件重命名为 tool_start，data 保持不变。摘要统一表示为 compact，usage 不属于事件表。尚未创建表或实施迁移。
- event 保留每条展示事件的完整具体内容，不只保存类型；普通对话及 continuation 调用产生的 thinking、text、tool_start、tool_result 明细均从该表取得。摘要需求使用 compact，摘要调用的实际输入输出由 model_call 保存；续写不生成新的摘要事件，其事件按普通调用及续接规则处理。
- event.data 的列声明使用 JSON，添加 CHECK (json_valid(data))。event.type=error 仅向前端传递模型或 run 自身的错误，工具错误统一由对应 tool_result.data.error 传递，不为同一工具错误重复生成 error 事件。thinking/text 等其他事件保持既定 data，不在 event 表另加 error 列。run、model_call、tool_call 原有 error 列仍保存各自错误，模型需要的错误 tool output 仍按原调用 ID 回填，不由前端事件替代。
- thinking、text 的 data 均使用 {content: "本次输出增量"}。error 事件按一次模型或 run 错误一条事件记录，data 为 {code, message}，不再携带工具专用 tool_call_id。同批多个工具分别最终失败时，各自通过原 ID 对应的 tool_result 反馈，不聚合为一条批量错误。工具最终失败后仍可将错误结果回填模型继续循环；工具临时失败等待重试期间沿用既定进度保存规则，不为其生成独立 error 或提前结束的 tool_result。事件本身不替代 end 表达 run 最终状态。

当前事件 data 契约汇总如下；各行均保存在 ai_events.data，并按原结构作为 SSE data 返回。

| type | data |
|---|---|
| start | {} |
| thinking | {content: "本次思考增量"} |
| text | {content: "本次正文增量"} |
| tool_start | {function: [{id, name, argument}]} |
| tool_result | {tool_call_id, output, error}；output 保持原值，没有则为 JSON null；成功 error 为 JSON null，失败 error 为 {code, message}；取消跳过使用 error.code=tool_skipped。全部结果均保存、分配 seq 并返回前端。 |
| error | {code, message}；仅用于模型或 run 自身的错误。 |
| end | {status: "succeeded" 或 "failed" 或 "cancelled"} |
| compact | {} |

- error 仍是 ai_events 中 type=error 的普通事件行，data 保存上述 JSON，按 run 内连续 seq 编号并在落库提交后推送、回放。SSE 的 id 使用 seq，不使用数据库自增主键 id；不创建独立 error 表或工具专用关联列。模型相关错误沿用 event.model_call_id 关联原调用；工具错误则与 tool_call.error、对应 tool_result 及 checkpoint 进度在同一结果事务中保存，正常推进时同时保存需要回填模型的错误 tool message。取消期间的工具错误同样使用 tool_result，模型或运行错误仍使用 error。
- 当 event.type=tool_start 时，data 固定采用 {function: [{id, name, argument}]}：function 为数组，每项 id 为模型返回的 tool_call_id，name 为工具名称，正常 argument 为从模型返回的工具 JSON 中解析得到的 JSON object；参数被截断、JSON 语法非法或合法 JSON 顶层非对象时，事件 argument 均为 JSON null，不保留原始参数片段、不增加 raw_argument。正常工具参数不采用旧 Mock 的 function.args={name, val} 结构，也不作为原始参数字符串增量保存。正常路径在工具开始执行时记录，不能因模型一次返回多个工具就提前记录尚未开始的工具；取消跳过的请求也须记录开始事件，与跳过结果配对。数组结构保持不变，一个事件仍只占一个 seq。
- tool_start 与 tool_result 必须严格对应同一次逻辑工具调用，包括未开始便取消跳过的调用：tool_start.data.function 中对应项的 id、ai_tool_calls.tool_call_id 以及 tool_result.data.tool_call_id 三者完全一致，均沿用模型返回的原调用 ID，不重新生成或改用数据库自增主键。两个事件的 run/session 及 model_call_id 元数据须对应产生该工具请求的同一模型调用；不能按工具名称或结果完成顺序配对，也不能用后续重新生成调用的结果替换旧调用。该规则由调用登记、结果结算和事件生成契约保证，不新增已否决的 tool_call_id 唯一约束。未开始便取消跳过时保留 skipped 事实，不因记录开始事件而改为 running 或伪造 tool_use。
- tool_result 的 data 固定为 {tool_call_id, output, error}，三个字段均保留。output 直接使用本次 tool_call.output 原值，保留对象、数组、字符串等实际 JSON 类型，没有输出则为 JSON null，不包装为助手正文或统一字符串；成功时 error 为 JSON null，最终失败时使用 tool_call.error 的 {code, message}，失败已取得的 output 仍保留原值，不用错误对象覆盖。取消跳过时 error.code=tool_skipped，message 说明实际跳过原因，该结果也发送前端。一个 tool_start 数组中的各工具分别产生自己的结果事件，按实际结果保存顺序取得 seq；并行工具允许交错完成，前端按原调用 ID 配对结果，并支持单个工具及多个工具的折叠展示。
- 保存工具结果时，在同一短事务中保存 tool_call.output/error、对应 tool_result 事件及原有 checkpoint/工具进度，事务提交后推送结果。协调器须先按产生顺序保存尚未刷新的事件；所有调用均保证对应 tool_start.seq 严格小于 tool_result.seq，包括取消跳过时同事务保存的两个事件。回放、重连或复用已保存结果不新增重复结果事件。tool_start 与 tool_result 均共享 run 内 seq 和 UNIQUE (run_id, seq)。工具最终失败、参数解析或校验拒绝即使没有 output，也通过原 ID 对应的 tool_result.data.error 传递错误，output 为 JSON null，不另发 error。取消跳过的事件同样与 skipped 状态和取消进度原子保存，提交后发送前端，不伪造工具业务结果。
- event.data 中解析后的 argument 与 tool_call.input 的职责不同；后者以 JSON 字符串编码保存实际收到的 arguments 原文，包含截断、语法非法及顶层非对象的参数，不因展示事件使用对象或 null 而改写。JSON 解析失败记录 tool_argument_parse_error；能解析但顶层非对象记录 tool_argument_validation_error。两类均保存 failed 的 tool_call、input/error，并按原 ID 回填错误、加入 completed_tool，不执行业务。能解析成对象但未通过 schema 校验时，事件仍保留该对象，错误同样使用 tool_argument_validation_error。
- 展示、回复拼接、续写归并、摘要各段排列及历史上下文组装均按 event 顺序进行，不再另用 model_call.id 排序；初次组装历史时从关联记录补齐原始响应、完成状态及执行结果，执行中持续保存完整 state.context。重启通过 checkpoint 关联调用的 input.messages 和 output 恢复，工具结果按已有 state.context 进度复用。事件顺序不替代 checkpoint 所拥有的执行恢复位置。
- thinking/text 事件采用增量追加，每条记录保存对应增量；续写通过读取及展示时合并实现，不覆盖旧 event 行。tool_start 事件按上述 function 数组保存工具调用，不沿用原始参数字符串增量的表示。SSE 按新事件原编号回放，续写合并不修改消费游标语义。被截断 tool 的调用事件 argument=null，不保存截断参数片段；后台 tool_call 记录实际 input 和解析 error，不执行业务。后续模型输入保留原调用身份，通过原 ID 的 tool output 说明参数解析异常、要求重新输入。
- 每条持久化事件都分配 run 内 seq，从 1 开始连续递增，重启后继续递增，不因重新请求模型或模型回复 id 改变而重置。事件使用同一 seq 作为 SSE 编号，查询及回放保留原编号；取消跳过的 tool_result 同样占用 seq 并发送前端，不过滤、不重新编号。
- 事件编号在持久化边界内分配、提交，同一 run 不得重复或产生未提交缺口，并行产生的事件按提交编号形成明确顺序。数据库记录与前端事件使用同一连续序列，不另编前端序号；例如取消跳过结果为 8、最终 end 为 9 时，前端按 7 → 8 → 9 接收。
- event.id 使用 INTEGER PRIMARY KEY AUTOINCREMENT，仅标识数据库记录，不代表事件顺序，不用于排列内容。seq 表示一次 run 中 event 的顺序，同时作为该 run 的 SSE 编号；添加 UNIQUE (run_id, seq)，保证同一 run 内 seq 不重复。续写、摘要及模型回复变化均不重置该 run 的 seq。
- 当前已定义事件均按 run 分配 seq 并传给前端，包括被截断的 tool_start、取消跳过的 tool_result 和取消收尾的 error；usage 及初始摘要正文仍不作为内部事件记录。tool 被截断不撤回已发送事件、不删除编号，也不重排历史序列；数据库行主键与 run 内事件顺序仍是不同概念。
- 内存用于流式解析和组装，按模型返回的回复 id 关联、拼装相应输出事件。
- 组装得到的事件内容先写入数据库，持久化成功后再发送给前端消费；持久化事件记录用于进程重启后的回放。正常路径的 tool_start 在工具进入 running/tool_use 的开始事务中编号保存并于提交后推送，不随模型参数流式输出提前生成；未开始便取消跳过时按取消规则与结果事件同事务保存推送；截断或非法参数使用既定 argument=null，随后在工具入口内校验失败，不进入业务操作。
- 流式事件按每 5 条 event 一组批量保存，不再以单个模型 chunk 作为分组边界；同一 run 内按产生顺序组批，每组使用一个短事务，事务内分配连续 seq 并写入全部记录，提交成功后再按 seq 依次推送前端。事务失败时不推送、不推进已提交序号；不同 run 的事件不混为同一顺序批次。
- 不足 5 条时，最早一条待保存事件最多等待 5 秒即保存本批已有事件；累计满 5 条则立即保存，不继续等待计时。后续事件到达不延后本批最早事件的等待期限，空缓冲不发起无内容写入。每次成功保存后，新一批从其首条事件重新计时。
- 模型调用结束、取消、出错或正常退出时，立即保存剩余事件，不等待满 5 条或 5 秒。start、compact、error、end 按对应事务立即保存，tool_start 随工具开始事务、tool_result 随工具结果事务立即保存；此前仍有待保存事件时须先按产生顺序保存，工具开始、结果、错误或控制事件不能越过这些事件取得更早的 seq 或提前推送。
- 模型调用结束时，未保存的尾批事件与该调用的结果、完成状态及相应 checkpoint 在同一短事务提交；普通调用结束使用 model_call_end，摘要流程继续时保持 compact，完整摘要成功按原子切换规则将 checkpoint 更新为下一普通调用的 model_call_start。取消及 run 终态仍遵循前文的原子提交要求。定时、满批、结束及控制事件触发的保存均由该 run 协调器串行处理，避免同一缓冲重复提交或重复推送。
- run 执行生命周期独立于前端 SSE 订阅；前端断线、关闭订阅或向前端发送失败不能触发 run 取消、失败或停止模型及工具执行。
- SSE 断线后后台继续执行并持久化事件；除正常完成及既定进程退出恢复规则外，业务推进仅因用户取消或执行错误而停止，不将无人订阅作为停止条件。
- 模型回复中途崩溃时，从 checkpoint 发起新的模型请求，使用新回复 id 解析、记录和推送新输出，不依赖旧 event 恢复模型执行。
- 活跃 run 重连时可携带最后成功消费的 SSE 编号，沿用 Last-Event-ID；后端回放编号严格大于该位置的事件，最后已消费事件本身不重复应用。
- 未携带游标时默认从起点回放 running 或 wait_for_cancel run 全部已保留事件，再接入最新实时事件，包括取消跳过的 tool_result、实际执行结果和收尾错误。wait_for_cancel 不恢复正常执行，但允许等待其唯一的最终 end。
- 历史回放完成后接入最新实时事件，回放与实时订阅的交接不得遗漏期间新提交的事件；前端按原 seq 排序、去重、检查连续性并保存重连游标。取消跳过结果照常发送，不因 tool_skipped 或工具当前状态过滤事件，不新增可见性列。Last-Event-ID 按原 seq 读取其后记录并接入实时流。
- 同一 running run 的有效新 SSE 订阅替换旧订阅：先完成请求、游标及 run 可见性校验，再原子切换订阅并关闭旧连接，不再因旧订阅仍存在而返回 sse_connection_exists。新订阅按自己的可选游标回放并接入实时流。
- 旧订阅的关闭、迟到回调或清理只作用于该订阅自身，不能清除新订阅或取消 run；订阅缓冲区满、前端写入失败也只结束传输，不使 run 失败。
- 消费游标只由前端按 run 管理，成功处理事件后更新；后端不额外保存各客户端的消费位置，也不以向连接写入或刷新成功作为已消费的依据。
- 前端游标丢失时可以不携带 Last-Event-ID，按无游标规则重新取得全部已保留的对外事件，包括被截断的 tool；回放起点不依赖后端客户端消费记录。
- run 已进入 succeeded、failed 或 cancelled 终态时，通过 run 概况查询及独立的事件分页查询还原完整展示内容，包括 input、output、thinking、tool call name 等，不再逐条回放终态 SSE，也不在单次 run 查询中加载全部事件。
- 新建或重连 GET /v1/run/:run_id/event 时，只有 run 已进入真正终态才返回 HTTP 204，前端随后查询完整展示内容。running 与 wait_for_cancel 均可订阅；后者等待工具完成或超时、run.cancelled 持久化后发送唯一取消 end，随后关闭连接，不提前按对外状态投影关闭 SSE。
- 详细模型调用记录与展示事件分别受各自契约约束；目标对外事件为 start/thinking/text/tool_start/tool_result/error/end 及 compact。摘要具体内容由 model_call 保存，usage 交由后端独立统计任务，不各自再建内部 event。
- start 的 data 为不携带业务 payload 的空对象 {}，照常落库、分配 SSE 编号并推送前端；模型或 run 错误通过 error 事件传递，工具错误通过 tool_result 传递。
- 每次需要进行摘要时即记录一条 compact，落库、分配当前 run 的连续 SSE 编号，提交后推送前端；data 为 {}，不再保留公共 error 字段。该事件记录摘要需求发生，只有其对应的完整摘要成功生效后，该位置才成为有效上下文分界。摘要调用关联由数据库元数据保存，不放入对外 data，也不把摘要正文或模型请求作为 payload 推送。
- 仅对明确发起的摘要 model_call 创建 compact；普通对话及 continuation 调用不创建该事件。compact.model_call_id 直接关联对应摘要调用，不因后续续写切换关联。
- compact 是 ai_events 中一条记录的事件类型值，不是新增名为 compact 的列。该记录包含 type=compact、对应 model_call_id、run_id、session_id、seq、data={} 及初始 compact_succeeded=false 等已定义字段。
- 摘要调用开始时，用同一个短事务保存三部分：ai_model_calls 中的摘要调用 input、running 及本次 max_retry；ai_events 中的对应 compact 记录并分配 seq；ai_checkpoints 中的 status=compact、model_call_id 及 state。事务提交后才发出模型请求及推送已持久化事件；恢复同一次调用复用原记录，不补建重复 compact。这样已持久化的摘要调用始终有对应事件身份，轮次统计不会误判。
- 同一次摘要模型调用的重试及重启恢复复用原 compact，不重复生成事件；续写新建 continuation=true 的普通 model_call，不新增 compact。摘要尚未完成或最终失败时保留已有事件，但不切换有效摘要或上下文分界；保留上一次有效摘要及其后的历史供后续使用，尚无有效摘要时保留全部候选历史。摘要最终失败的当前 run 结束为 failed，不再继续普通模型调用。完整摘要成功后启用对应分界，不因标记成功而追加事件，也不改写 compact 已有的对外 data 及编号。
- compact 事件仅用内部布尔字段 compact_succeeded 表示对应摘要是否成功生效，创建时为 false，完整摘要生效时改为 true；未完成、失败或取消均保持 false，不为事件增加摘要专用状态枚举、错误列或执行进度字段。checkpoint.status=compact 单独表示执行阶段。其他类型事件的 compact_succeeded 为空；SQLite 使用 INTEGER 保存 0/1。compact_succeeded 不加入对外 data；摘要错误通过独立 error 事件传给前端，不回写已发送 compact 的 data，执行详情及恢复信息仍由 model_call、run 和 checkpoint 记录负责。
- event.model_call_id 保存产生该事件的本地模型调用关联，引用 model_call.id，且须属于相同 run/session；非模型调用相关事件可为空。该关联为内部元数据，不加入 SSE data。每条 compact 直接关联其对应的摘要 model_call，关联建立后不切换为后续调用；续写产生的事件关联续写自身的 model_call。有效摘要正文按既定规则拼接并进入普通调用 input，重启恢复入口见 checkpoint 读取规则。
- 返回前端的事件包含 start、end、compact 等控制事件、独立 error 事件、tool_start 及 tool_result；前端正常消费控制事件及其编号但不渲染其内容，展示 thinking、text、工具调用及原始结果、error，包括被截断的调用事实和实际错误。compact 不显示此前的固定压缩提示文案。tool_start 是工具请求的开始侧记录，本身不证明已经处理或业务生效；正常路径随开始处理登记，未开始便取消跳过时也发送该事件，前端结合调用结果及 tool_skipped 展示实际处理或跳过事实。工具调用及结果按当前模型回复内对应原 ID 支持单个和多个工具折叠。
- usage 从实际模型 chunk 的字段提取，通常出现在最后一个 chunk，随调用结果保存到 ai_model_calls.usage，独立统计任务按需读取；若同一 chunk 还携带文本或结束信息，分别解析，不把 usage 假设为独立 chunk 或生成 usage event。

### 事件编号与可见性清单

| 事件来源与类型 | 分配 seq 及 SSE 编号 | 推送前端 | 显示事件内容 |
|---|---|---|---|
| run 开始事件 start | 是 | 是，data 为 {} | 否，消费生命周期信息 |
| 正常对话 thinking、text、tool_start、tool_result | 是 | 是 | 是 |
| 取消跳过工具的 tool_result | 是 | 是，查询也返回 | 是，按原调用 ID 展示跳过原因；模型上下文保留调用并回填 content="aborted" 的 tool 消息 |
| 被截断的 tool 记录 | 是 | 是 | 是；argument=null，创建保存 input/error 的失败 tool_call，不执行业务，按原 ID 回填错误 tool output |
| 错误事件 error | 是 | 是 | 是，展示错误；不因收到该事件就结束 run |
| 取消收尾期间的 error | 是 | 是，查询也返回 | 是，在最终取消 end 前按序展示 |
| run 结束事件 end | 是 | 是 | 否，处理结束状态；模型或 run 错误由 error 提供，工具错误由 tool_result 提供 |
| 摘要事件 compact | 是 | 是，data 为 {} | 否，记录摘要触发；对应摘要成功生效后才作为上下文分界 |

被截断的 tool 以 argument=null 的事件保留、编号、推送及回放，事件不携带截断参数片段；后台创建 failed 的 tool_call 记录 input/error。后续模型输入保留原调用 ID，以对应 tool output 说明参数解析异常、需要重新输入，不用独立普通文本消息替代，不执行业务。错误结果保存后该 ID 加入 completed_tool。摘要触发使用 compact，初始摘要调用的正文保留在 model_call；continuation 调用产生的普通事件遵循续写规则。usage 由后端独立统计任务处理，不作为 event；model_call、tool_call 和 checkpoint 本身也不是 SSE 事件。

### 模型标识的边界

- [DeepSeek Chat Completions 官方文档](https://api-docs.deepseek.com/api/create-chat-completion/) 将返回的 id 定义为一次 completion 的标识，同一流的多个 chunk 共享该 id；它不是逐条流式事件的唯一标识。
- 上述文档未提供重新请求后沿用原 completion、重放原事件的恢复保证。因此不能以模型返回的 id 作为跨请求增量去重的依据。
- 回复 id 用于关联同次模型回复，不能单独作为其中每条事件的唯一标识；事件以自增 event.id 标识数据库行，以 run 内的 seq 表示顺序并使用 UNIQUE (run_id, seq) 保证不重复。模型调用等非事件记录不使用 SSE 编号，event.id 也不承担事件排序职责。
- 新旧回复分别关联各自的模型回复 id；前端按已保留事件顺序展示旧片段与新输出，不替换旧片段，也不以展示内容作为执行恢复的入口。
- 流式事件满 5 条或最早待保存事件等待满 5 秒即用短事务保存；调用结束、取消、出错、正常退出及控制事件的即时保存规则已确定。其余事件与 checkpoint 的具体提交边界继续按各执行阶段细化，不再将上述批量大小及刷新时机列为待定。

## 模型上下文的有效内容

- 组装模型历史时，使用当前 session 最新有效摘要与其后尚未覆盖的全部有效消息；没有有效摘要时使用该 session 的全部候选历史。新 run 沿最新适用 checkpoint 及其关联 model_call 的实际消息快照接续，不根据 run 创建时间重新拼入已覆盖原文；当前轮真实输入等未压缩消息仍保留。触发摘要时，除 system prompt 外的全部有效历史都参与压缩，原始记录不物理删除。
- seq 表示 run 内事件顺序，每个 run 从 1 开始；同一 session 跨 run 读取沿用已有会话历史契约中的 run 创建时间升序，再按各 run 的 seq 排列，不直接混排不同 run 的局部 seq，不使用 event.id 或 model_call.id 排序。分界引用须能定位所属事件及 run。
- 固定 system 提示继续保留；有效摘要从关联调用和事件取得，再加入尚未被摘要覆盖的有效消息、新 run 的真实用户输入和后续实际消息。正在进行的续写保留 assistant:a、user/tool:b、assistant:c 的实际消息结构，其中 a 始终是此前模型输出，b 是后端按截断类型生成的请求；不能将有效摘要的 user 投影约定套用于续写中的 a。start、end、compact 是控制记录，error 是前端错误记录，均不直接作为模型对话消息；模型需要的工具错误仍从对应 tool output 进入上下文。
- 未被摘要覆盖的用户消息从对应 run.input 取得，工具执行结果从 tool_call 补齐；事件范围选择不替代此前确认的完整回复检查、跨 run 部分输出处理和取消跳过工具的 aborted 消息回填，也不改变 checkpoint 作为执行恢复位置唯一依据的规则。
- 定位摘要时，只使用 compact_succeeded=true 的 compact；false 的事件仍保留，但不能作为有效摘要。通过 event.model_call_id 关联其对应的摘要调用；含续写时按既定正文拼接规则形成 user 摘要，不以事件存在本身作为生效依据，也不从所属 run 推导固定历史保留窗口。
- 同一个 run 内，只有响应完整接收并已持久化确认的模型调用，才能推进下一轮循环；网络异常中断的回复即使已有 event 记录，也不能据此执行工具或继续循环。正常长度结束信号按自动续写规则处理。
- 用户在上一 run 取消或失败后重新发送消息时，允许新 run 将上一轮部分输出作为历史上下文；这是跨 run 继续对话的规则，不代表旧回复已完整接纳。
- `model_call` 保存完整输出及回复接纳的完成状态；event 只保留详细增量用于展示和回放，不再追加单独的内部完成标记。
- 模型调用进入 succeeded、failed 或 cancelled 时，状态、已有输出、错误与对应 checkpoint 更新在同一事务中提交，避免结果与执行恢复位置不一致。
- 回复被接纳的事实随 model_call 保留，独立于后续覆盖的 checkpoint，使历史上下文仍能识别有效回复。
- 正常循环的上下文选择 succeeded 的 model_call 输出及对应工具结果；新 run 接续中断对话时，按上述跨 run 规则纳入 failed/cancelled 的部分回复，并保留其未完成事实。
- 长度截断的 tool 保留在模型消息中的 assistant.tool_calls，并使用原 tool_call_id 配对一条 role=tool 消息，内容说明工具参数解析异常、要求重新输入完整调用；不将原 ID 作废，不以独立普通文本消息替代该结果。后台 tool_call 保存实际 input 及解析 error，错误消息、completed_tool 和 checkpoint 与失败记录原子提交。model_call.output 按实际响应规则保存，event 仅以 id、name、argument=null 保留调用事实，不保存截断参数片段。
- 实际纳入下一次模型请求的 assistant.tool_calls 必须与其 tool message 一一对应，包含完整调用和参数被截断的调用：成功提供实际结果，最终执行失败提供实际错误，参数校验或截断解析失败提供对应错误。所有结果使用原调用 tool_call_id；不能以虚构业务执行结果替代校验错误，也不根据模型文字推断操作是否生效。已由有效摘要覆盖的记录遵循既定摘要规则，不重复上传原始内容。
- 消息序列约束：assistant 消息包含 tool_calls 后，紧接着仅追加该批调用对应的 role=tool 结果消息，每个 ID 都须有结果，全部补齐后再进行后续模型交互；不能在尚未补齐的调用与结果之间插入 user 或 assistant 消息。该约束针对请求中的消息序列，不限制模型下一次只能输出工具调用，模型仍可回复正文或再次调用工具。工具结果的 role、tool_call_id 及结果回传示例见 [DeepSeek Tool Calls](https://api-docs.deepseek.com/guides/tool_calls/) 和 [Chat Completions API](https://api-docs.deepseek.com/api/create-chat-completion/)。
- 模型协议到存储的映射如下；模型 assistant 消息的 tool_calls 数组、展示事件的 data.function 数组与逐条工具执行记录分别按各自职责保存，不把供应商 arguments 字符串直接当成展示事件的参数对象。

| 模型协议内容 | 事件及工具记录 |
|---|---|
| tool_calls[i].id | data.function 中对应项的 id，以及 tool_call.tool_call_id；role=tool 的 tool_call_id 引用同一值。 |
| tool_calls[i].function.name | 对应事件函数项的 name，以及 tool_call.name。 |
| tool_calls[i].function.arguments | tool_call.input 保存该字符串原文的 JSON 字符串编码；解析所得对象放入对应事件函数项的 argument，截断、语法非法或顶层非对象时该事件字段为 null。 |
| 工具实际结果 | tool_call.output 保存完整实际结果，按调用 ID 生成 role=tool 消息；模型侧 content 按下述长度规则裁剪后，将实际消息完整保存到 checkpoint.state.context。失败时由 error 提供对应的错误结果。 |

- 工具结果进入模型上下文时，在内存中按 ceil(UTF-8 字节数 / 4) 估算待回填 role=tool 消息 content 的 token 数；超过设定的 token 阈值则省略中间部分，保留头尾内容，并在 content 内插入明确的中间内容已省略、结果不完整说明。单条工具结果的裁剪阈值默认 8192 个估算 token，允许用户配置，纳入现有 config 统一加载管理，由 default.yaml 提供默认值、config.yaml 提供用户配置。裁剪后的头尾及省略说明合计须满足该 token 阈值，默认上限对应最多 32768 个 UTF-8 字节，裁剪位置保持 UTF-8 字符完整。沿用原 role、tool_call_id 和 content 消息结构，不增加 truncated、head、tail 或其他 JSON 字段，不新增数据库裁剪标记或计数列。具体配置键及有效范围继续细化。
- 头尾保留比例固定为 1:1，不新增比例配置项。先从 token 阈值对应的字节上限中扣除省略说明的 UTF-8 字节数，再将剩余空间按字节平分给头尾；截取边界向内收缩到完整 UTF-8 字符，允许因此少保留少量字节，最终 content 仍须满足阈值。
- 裁剪只作用于模型消息 content 的文本投影；tool_call.output 和前端 tool_result.data.output 仍完整保存并返回原始结果，工具状态和 error 仍按真实执行结果记录。content 内被裁剪的 JSON 文本仅作为结果片段，不通过 JSON 格式补全将其伪装成完整业务对象；外层请求 JSON 仍按原消息结构正常序列化。
- 实际回填的 tool 消息按原工具结果事务与 completed_tool、last_tool 和 checkpoint 一起保存，后续 model_call.input 保存模型实际收到的同一消息；恢复及结果复用直接沿用已保存 content，不因配置变化重新裁剪或重复回填。裁剪后再估算整个请求的输入预算，仍超预算时按运行中 compact 规则处理；省略说明表达结果视图不完整，不表示业务操作失败。

以两个不同 ID 的 get_weather 调用和一个 get_time 调用说明协议：共形成三条 tool_call，每个调用单独保存 input 及结果，回传模型三条与原 ID 配对的 role=tool 消息。展示事件的 function 数组可以包含这三个函数项；同名工具按各自 ID 分别处理，不能按工具名合并成一次调用。

- 取消跳过的调用保留在 state.context 及后续历史上下文的原 assistant.tool_calls 中；紧随该批请求回填 role=tool、tool_call_id 为原调用 ID、content 为字符串 "aborted" 的消息，与同批其他工具结果一并配对齐全。取消 run 不因此再调用模型，后续新 run 读取历史时可使用该消息；恢复时复用已保存的 aborted，不重复追加或执行工具。取消跳过事实与前端 tool_result 分别按既定结构保存；截断解析错误仍回填实际解析错误，不用 aborted 替代。
- failed 完全没有输出时，不为其构造空的 assistant 消息；已有用户输入及真实调用错误记录仍保留。仅正文为空但已收到 thinking 时，仍按上述规则保留包含 reasoning_content 的 assistant 消息，不视为完全没有输出。

### 上下文摘要

- 同一 run 内触发的 compact 一并压缩当前有效上下文中的旧摘要、全部历史消息和当前 run 已完成的模型回复及工具交互；固定 system 与本轮真实用户输入原文保留，不纳入本次压缩。工具请求与对应结果按完整批次处理，不拆开配对关系。已完成的业务操作、工具记录及展示事件保持原样，不因压缩重新执行。
- 运行中摘要生成阶段的输出仍是该摘要 model_call 的实际 assistant 输出；完整成功后，将摘要正文作为一条独立的 role=user、content 为摘要正文的消息传入后续普通模型请求。含续写的摘要仍按既定正文拼接规则形成这条 user 消息，不将 thinking 或摘要专用 system prompt 放入摘要正文；不覆盖 run.input、不创建新 run，也不将历史摘要的角色转换套用到尚未完成的摘要续写。
- 运行中 compact 成功后，普通模型请求的消息顺序为固定 system、role=user 的新摘要、本轮真实 user 输入，随后追加本次压缩之后实际产生的 assistant/tool 等消息。旧摘要和本次已覆盖的历史、模型及工具消息不再重复加入；工具定义仍按正常请求提供，不作为上述 user 摘要的一部分。完整有效消息保存到 checkpoint.state.context 和后续 model_call.input.messages；成功切换、失败、取消及恢复仍使用既定原子事务与 checkpoint 规则。后续新 run 沿该有效上下文接续未覆盖消息及新输入，不重新按 run 数选取已覆盖的历史原文。
- 在创建新 run 前检查当前上下文的预计输入 token 量，达到可用输入预算阈值时安排摘要压缩。同一 run 的后续普通调用不因达到摘要触发阈值而重复触发创建前摘要入口；工具结果使输入超过实际可用预算时，允许触发下述运行中 compact。检查到创建前需要摘要后，先保存状态为 running 的新 run，再在该 run 下登记摘要 model_call、compact 及 checkpoint；登记事务提交后才发出摘要请求。摘要及其续写、取消、错误与恢复均归属这个新 run，不写入此前旧 run。
- 以创建 run8 为例，创建前检查 run1 至 run7 形成的全部有效历史；触发摘要时，这些历史除 system prompt 外全部进入压缩，已有摘要代替其覆盖原文。随后保存 run8，并在短事务中保存归属 run8 的摘要 model_call.input/running、本次 max_retry、compact 及 checkpoint.status=compact/model_call_id，提交后调用模型。摘要完整成功后，组装普通上下文并检查可用输入预算，预算内才切换普通调用；摘要最终失败或成功后仍超预算时，run8 结束为 failed，后者仍保留成功摘要。run8 本轮真实用户输入保持原文，不属于创建前待压缩历史。
- 触发阈值可配置，用户未设置时默认 80%；可用输入预算已预留模型输出空间，不按固定对话轮数触发摘要。
- 每个模型配置 context_window 与 max_output_tokens；可用输入预算为两者之差，输入量包含固定提示、工具定义及消息内容。
- 一次摘要任务从独立的 model_call 开始；长度截断时可以通过新的 model_call 续写，各次均保存实际调用输入输出，不消耗 Agent loop 的轮次上限。
- compact 不支持将历史拆成多段分别摘要，也不做多级摘要归并；基于所选模型能够承载待压缩历史的前提，将全部有效历史作为同一次摘要任务的输入。摘要请求发生实际错误时沿用既定失败与重试规则，不自动切换为分段处理。长度截断后的 continuation 是对同一摘要的续写，仍按既定 max_continue 规则处理，不属于历史分段摘要。
- 完整摘要正文的预期输出规模为约 10k token 以内或附近，含续写时按最终拼接正文看待该规模；按既定 UTF-8 字节估算方式评估。该规模是摘要输出目标，不新增精确 10000 token 的强制截断、拒绝或再摘要规则，也不改写固定专用 system prompt。实际每次模型调用仍使用所选模型配置的 max_output_tokens；该配置未设置时的 8192 默认值与既有续写规则保持适用。
- 创建前及运行中 compact 调用使用以下固定的专用 system prompt，按原文发送，不沿用普通对话的 system prompt，也不追加其他提示内容。以一条 user 消息承载待压缩历史，模型返回的摘要段属于 assistant 输出。摘要正文或 thinking 截断时，保留该 assistant 消息 a，再追加后端的 user 续写请求 b“继续未完成的输出”，随后接收新的 assistant 输出 c，续写调用仍使用同一专用 system prompt。

```text
你正在执行一次上下文检查点压缩。

请为后续接手任务的另一个大语言模型生成一份交接摘要，使其能够继续当前任务。

摘要应包含：
1. 当前任务的进度，以及已经做出的关键决策。
2. 重要的上下文、约束条件和用户偏好。
3. 尚未完成的工作，以及明确的后续步骤。
4. 继续工作所必需的关键数据、示例和参考资料。

摘要应简洁、结构清晰，专注于让接手的模型能够顺利继续工作。
```

- 摘要内容要求保留输入历史中的五类信息：用户目标与约束、相关日程标识、工具实际结果、未完成事项、待澄清问题。输入没有的信息不得编造；相关标识按原值保留，工具结果压缩后仍须保留影响后续操作的事实。
- 摘要必须区分已经执行的操作与仅计划执行的操作，成功、失败、取消跳过及结果未确认按实际记录表达；不能将助手的意图、思考、未完成表述或工具参数格式补全视为操作成功。结果未确认时保留不确定性，不断言业务已经生效或一定未生效；未完成事项和待澄清问题不得因压缩被写成已完成或已解决。
- 创建前触发 compact 时，当前会话已有的全部有效历史除 system prompt 外均纳入摘要输入；不按 run 数保留任何历史原消息，不设置固定保留轮数。已有有效摘要时，输入是旧摘要与尚未覆盖的有效消息，不重新展开已被摘要替代的原文。本轮真实用户输入不是已有历史，仍原样保留供压缩后的普通模型请求使用；历史记录不物理删除。
- 摘要输入从完整有效历史视图取得，不按固定 run 数或 message 条数切片；同一批 assistant.tool_calls 与其全部 tool 结果必须成组纳入，不能只压缩调用或结果的一侧。创建 run8 时，首次摘要覆盖 run1 至 run7 的全部有效消息；后续摘要覆盖当前旧摘要及全部尚未覆盖的历史消息。
- 创建新 run 前，确实没有可压缩的有效历史时，即使输入估算达到摘要触发阈值，也跳过摘要，不创建摘要 model_call 或 compact；不能仅因已有 run 数较少而跳过。输入仍在可用预算内时继续普通调用流程；若输入已超过可用输入预算，则仍创建本次 run，直接保存 status=failed、原用户 input、error.code=context_budget_exceeded、output=NULL 及完成时间；同一短事务保存 seq=1、2、3 的 start、error、end，end.data.status=failed，error 说明输入超预算且无可压缩历史。不创建 model_call、checkpoint 或 compact，不发出模型请求。该失败保留请求幂等键，相同键和 input 返回原失败 run；遵循原有准入预留及提交后释放规则。
- 已有有效摘要时，从当前有效历史视图构造下一次摘要输入，将此前摘要与其后全部尚未覆盖的有效历史一起重新摘要；不重新上传已被摘要替代的原始历史，不因 run 增多而跳过尚未覆盖的中间消息。摘要 user 投影、续写正文拼接及 input/output 恢复入口均按已定规则处理，不新增固定历史窗口或重复边界字段。
- 摘要续写新建普通 model_call 并设置 continuation=true，追加 user 消息“继续未完成的输出”；checkpoint.model_call_id 更新为本次续写调用，status 保持 compact，重试和结果保存期间也不切到普通调用阶段，不额外新增专门的续写关联外键。
- 摘要复用当前 run 的模型，不单独配置摘要模型；通过调用用途及提示词区分摘要与正常对话。
- 摘要及其续写请求禁用工具，只允许生成摘要内容；不向这些请求开放可执行工具，摘要输出不能触发日程业务操作。
- 正常对话调用与摘要压缩调用须明确区分，防止摘要被当作助手面向用户的正常回复。
- 摘要需求发生时记录 compact，直接关联其对应的 model_call；事件落库并分配 SSE 编号，对外 data 为 {}，前端消费但不显示事件内容，错误通过独立 error 事件传递。实际模型调用及续写各自保存在 model_call；续写使用 continuation 标记，不单独记录 compact。仅在完整摘要成功生效后启用对应分界。
- 摘要需求在 event 表中表现为 compact，具体输入输出保存在对应 model_call 中；续写视为普通模型调用，其 thinking、text、tool_start、tool_result 等事件按通用续接规则处理，不再沿用摘要续写完全不产生普通事件的旧约定。摘要仍禁用工具，工具事件类型的通用约定不表示允许摘要执行业务工具。usage 不作为事件记录，交由独立后端统计任务处理。
- 摘要是 session 级可复用的历史内容；摘要及续写调用仍关联触发它的 run 和 session，各段正文保存在对应 model_call.output 中，不改写先前调用的实际输出。
- 摘要完整生成后才按关联 event 的顺序组织本次有效摘要及相关调用，不按 model_call.id 排列各段。摘要生成和续写期间，模型输出保持 assistant，后端续写请求保持 user，形成 assistant:a、user:b、assistant:c；不采用此前误解的 user:a、assistant:b、assistant:c，也不因拼接正文而省略后端续写请求。生成过程中不替换 session 已有摘要，部分摘要不得作为有效历史输入；各次 model_call.input/output 和 event 原样保存。session.compact_event_id 定位有效摘要；其完整 user 投影进入后续 model_call.input 后，重启直接沿 checkpoint 读取该输入，不再要求从 compact 遍历摘要调用链以恢复基础上下文。
- ai_sessions 使用可空 INTEGER 列 compact_event_id 引用最新成功的 ai_events.id；目标必须属于同一 session，类型为 compact 且 compact_succeeded=true。尚无成功摘要时为 SQL NULL；不额外保存重复的摘要调用引用、run_id 或边界 seq，这些信息从关联事件取得。event.id 仅用于定位，不承担顺序语义。
- 创建前摘要压缩后的普通对话上下文归属于新 run，消息顺序为普通 system prompt、role=user 的新摘要、新 run 的真实用户输入及其后实际消息；本次已覆盖历史不再以原消息追加。摘要模型实际返回的 role 为 assistant，各次 model_call.output 保持实际响应，仅将完整有效摘要正文投影为普通调用的 user 消息；不改变摘要生成或续写时的 assistant:a、user:b、assistant:c。
- 以创建 run8 为例，若触发压缩，则 run1 至 run7 的全部有效历史形成摘要，普通模型调用的 messages 依次为普通 system prompt、role=user 的有效摘要、run8 本次真实 user 输入，随后追加 run8 的 assistant/tool 等实际消息。摘要专用 system prompt 不进入普通调用消息序列。完整 messages 保存在该普通 model_call.input 与 checkpoint.state.context，不覆盖 run8.input，不新增上下文字段。
- 未被最新有效摘要覆盖的消息全部按既定顺序保留，已覆盖原文不补回。摘要生成中的原始 assistant 回复与生效后作为 user 使用的摘要属于不同阶段；摘要经过续写时，按相关 event 顺序拼接摘要正文，作为普通 system 后一条 user 消息的 content，不包含 thinking、后端续写指令或摘要专用 system prompt。各次 model_call.input/output 和 event 分别保留；恢复以 checkpoint 关联调用的 input.messages 为基底，未完成的 continuation 按原流程处理，不新增摘要调用链全量回放要求。
- 已被摘要覆盖的用户消息不再额外原文重发；摘要后的新用户输入及模型、工具消息按实际顺序继续组装。
- 生成摘要不改变原有会话记录；已保存的历史消息、调用及事件记录原样保留，不删除或改写。新增摘要 model_call，更新摘要引用及恢复元数据，不将摘要写回覆盖原始内容。
- 摘要需求发生时持久化对应 compact 及恢复进度，同一次模型调用恢复后复用该事件。摘要完整成功并准备继续普通调用时，在同一个短事务中保存最后一次摘要调用的 output、完成状态及尾批事件，更新 session.compact_event_id 与原 compact 的 compact_succeeded=true，并登记下一次普通 model_call 的完整 input、running 和 max_retry；checkpoint.model_call_id 指向该普通调用，status=model_call_start，state.context 为组装好的普通上下文，completed_tool=[]、last_tool=""。事务提交后才发出普通模型请求，使有效摘要和下一次普通调用的恢复入口同时生效，不留下摘要已提交而普通调用未登记的持久化中间态。中途长度截断只保存本次摘要结果及续写恢复进度，不提前更新有效摘要或登记普通调用。摘要最终失败时保留 compact 且 compact_succeeded=false，原 compact_event_id 及有效分界不变。
- 上述摘要成功后登记普通调用的原子切换仅适用于组装后的输入仍在可用预算内。若完整输入估算超过 context_window - max_output_tokens，则保存成功摘要并更新 session.compact_event_id 和 compact_succeeded=true，本次 run 以 context_budget_exceeded 结束为 failed；不登记或发送普通 model_call，不递归摘要。创建前及运行中摘要均按固定提示、工具定义、新摘要、本轮真实输入及其他实际未覆盖消息检查预算，不补回已覆盖原文。摘要调用仍为 succeeded，不将本次运行预算错误记为摘要生成失败。
- 摘要成功但普通输入超预算时，在同一短事务保存最后一次摘要调用的结果、usage 及尾批事件、成功摘要引用、checkpoint、run.failed/error/finished_at 以及 error/end 事件；checkpoint 保持 compact，指向最后一次摘要调用，不伪造普通调用开始。run.output 没有普通回复时为 SQL NULL，不以摘要正文填充；error 使用 context_budget_exceeded，说明摘要后输入仍超预算。事件沿用当前 run 的连续 seq，error 先于唯一 end，提交后推送并释放活跃名额；幂等重放返回原失败 run，重启不恢复终态执行。
- 摘要调用或其续写最终失败时，当前 run 结束为 failed，不回退到原始上下文继续普通模型调用。模型错误重试与长度续写仍遵循既定规则：可重试且未产生有效输出时使用剩余重试预算，长度截断时使用剩余续写预算；预算耗尽、不可重试错误或已有部分有效输出后异常中断均按对应最终失败规则收尾，不为摘要另开重试机制。保留旧有效摘要、原始历史、本次摘要及续写实际已接收内容；未完成摘要不作为后续有效摘要或普通助手回复使用。最终错误、调用结果、checkpoint、run.failed 及唯一 end 按既定终态事务规则提交，error 先于 end 推送；用户取消仍优先遵循 cancelled 流程。
- 摘要调用无须因没有业务工具而结束 run；执行器按调用用途区分摘要与正常回复，摘要成功且组装后输入在预算内时继续原本的对话流程，超预算时按上述失败分支收尾。
- context_window 必须明确配置，max_output_tokens 未配置时默认 8192；校验 0 < max_output_tokens < context_window。
- 预算配置缺失或非法时，仅将对应模型标记为不可用，不影响其他有效模型、日程和提醒。
- 发送前使用可替换的 token 估算器，只估算本次 model_call.input，包含其中的固定提示、工具定义及消息，不估算尚未生成的 output。第一版统一按 4 个 UTF-8 字节估算 1 个 token，公式为 estimated_tokens = ceil(byte_count / 4)，整数实现可用 (byte_count + 3) / 4；空输入估算为 0。整体请求按实际 model_call.input 序列化后的完整 UTF-8 字节数计算，单条工具结果按其 content 文本的 UTF-8 字节数计算；两者复用同一算法，不按字符数或各片段分别向上取整后求和。该值为运行控制的启发式估算，不作为真实 usage。
- 估算结果仅在内存中作为自动压缩触发条件，不写入 model_call、event 或其他数据库字段，不作为真实 usage 使用。
- 同一 run 执行工具后，先按单条工具结果的长度规则形成模型侧消息；若回填后下一次普通模型请求的整体输入仍超过可用输入预算，先进行一次 compact，再检查压缩后的完整输入；该 compact 不计普通轮次，也不消耗额外 compact 次数预算。compact 覆盖旧摘要、全部有效历史及当前 run 已完成的模型与工具交互，固定 system 和本轮真实输入原文保留，成功摘要正文以 user 消息进入后续模型输入。一次 compact 后仍超预算则按 context_budget_exceeded 失败，不在同一步递归压缩。后续新 run 沿用有效摘要及全部未覆盖消息，不补回已覆盖原文，不裁剪原始记录或重新执行工具。
- 真实 usage 以模型 API 返回值为准，原始值保留在 model_call 中，需要查看统计时由独立任务按需读取并重新计算，不定时后台累计，不生成 usage event；没有返回时保持缺失，不用估算值或虚构的零值补齐。
- 每次创建新 run 的请求最多启动一次创建前自动摘要任务；工具结果导致实际超预算的运行中 compact 另按上述规则触发。摘要任务因长度截断而续写不算再次压缩，摘要及其续写调用自身不触发自动摘要。
- 摘要成功后检查组装好的完整输入是否超过可用输入预算；未超过时直接使用新上下文继续原计划的普通调用，即使估算值仍高于 80% 等配置的摘要触发阈值也不在同一步递归压缩；超过实际预算则按 context_budget_exceeded 失败分支收尾。摘要触发阈值与实际输入预算分别判断，下一次创建 run 前再检查摘要阈值。
- 摘要后恢复也须遵循同一步不重复压缩的规则，具体恢复引用随 checkpoint 设计落实。
- checkpoint 各阶段恢复示例尚待确定；UTF-8 字节数除以 4 并向上取整的统一 token 估算算法、先检查再保存新 run、摘要调用及 compact/checkpoint 归属触发 run、除 system prompt 外全部有效历史参与摘要、本轮真实输入原样保留、后续新 run 沿摘要及未覆盖消息接续、摘要正文拼接及 input/output 恢复入口、无可压缩历史时跳过摘要、超预算直接创建 failed run、摘要最终失败后终止本次请求均已确定，不再列为未定流程。

## 用量统计

- model_call 中持久化的 API 原始 usage 是独立统计任务的数据来源，包含普通对话、摘要及续写调用实际获得的用量；不从 SSE 展示事件或 token 估算值推导真实用量。
- 独立统计任务按需触发：需要查看统计时，读取对应查询范围内的 ai_model_calls.usage，每次从这些记录重新计算，不在旧统计结果上追加累计；重复执行不会重复计数。重启后仍沿用相同读取计算方式，不需要启动补算、定时后台累计任务或新增“已统计”字段。统计过程不推进 Agent loop、不更新执行 checkpoint，也不生成 usage event。
- usage 的持久化位置统一为 ai_model_calls 的独立 usage JSON 可空列，保存模型返回的原始 usage 对象，不另建统计表，不采用仅保存在内存中的方案；统计任务读取该列，不要求解析整份 output。
- 未返回 usage 的调用保持未知，usage 列为 SQL NULL，不以零值或估算值补齐。不增加按 run、session、provider、model 四个维度的汇总功能；统计的按需触发及重新计算规则已确定。

## Run 查询的完整展示与失败输出

- 终态完整展示需要 run 的用户 input 与 event 表中按序保存的 thinking、text、tool_start、tool_result 等明细；run.output 保留最后一轮正文及其按 event 顺序拼接的续写结果，不充当整个 run 的完整历史。例如 A 经续写得到 B 时保存 A+B，原始增量及调用记录仍分别保留。
- 工具执行结果需要展示给用户，后端通过 tool_result.data.output 直接返回 tool_call.output 原始值，保持原有 JSON 内容与类型，不改写为摘要或助手解释文本。前端支持折叠展开单个工具，并支持多个工具的整体折叠展示；tool_start 保存原调用 ID、名称及参数，tool_result 通过严格一致的原调用 ID 配对。查询 events 返回持久化的结果事件，实时 SSE 推送同一记录及 seq；不将结果伪装成模型 text 输出。tool_result.data 固定保留 tool_call_id、output、error；成功 error=null，最终失败 error={code, message}，没有 output 时为 null，不再另发 error 事件。
- GET /v1/run/:run_id 直接返回 run 的受控概况对象，不包额外 run 层、不内嵌事件列表。固定十个字段为 run_id、session_id、provider、model、input、status、output、error、created_at、finished_at，字段始终存在：前五项为字符串，status 为既定对外状态，output 为字符串或 null，error 为 {code, message} 或 null，created_at 为 RFC 3339 北京时间字符串，finished_at 为同格式字符串或 null。input 仅为本轮真实用户输入，output 保留既定最后一轮正文及续写投影；失败已有正文时仍返回，不把完整事件或模型上下文塞入 run 字段。
- 事件明细独立通过新增 GET /v1/run/:run_id/events 分页查询，与已有单数路径 /event 的实时 SSE 订阅区分。两个查询共同支持完整展示，不将事件列表作为 run 的存储字段，也不恢复一次返回 {run, events} 全量详情的方案。对外 wait_for_cancel 仍投影为 cancelled，内部未结束时 finished_at 为 null，不能仅据对外状态判断取消收尾已经完成。
- GET /v1/run/:run_id/events 使用 after_seq 游标分页；after_seq 为非负整数，首次为 0，未提供时默认 0。只读取当前 run 内 seq > after_seq 的事件，按 seq 升序最多返回 limit 条，不采用 offset 分页；后续请求沿本页最后一条事件的 seq 继续，空页不推进游标。游标表示 run 内已读位置，不是数据库 event.id；limit 未提供时默认 10，必须为 1 至 100 的整数，最大 100。参数非法或超范围时返回 HTTP 400、error.code=invalid_request，不静默截取或修改请求页长。
- 事件分页响应固定为 {events, next_after_seq, has_more}，三个字段始终存在。events 是当前页事件数组，无记录时为 []；next_after_seq 是本页最后一条事件的 run 内 seq，空页时等于请求的 after_seq；has_more 是布尔值，仅表示当前已持久化事件中是否还有后续页，不表示 run 是否结束。运行中 has_more=false 后仍可继续产生新事件，前端通过 run 概况或 SSE end 判断运行结束；查询只读，不推进执行或消费状态。
- events 中每条事件固定为 {seq, type, data}，三个字段始终存在。seq 为当前 run 内的正整数事件序号，与同一事件的 SSE id 完全一致，不返回数据库 event.id，也不增加 run/session/model_call 等内部关联字段。type 与 data 完整沿用前文八种事件定义：start、thinking、text、tool_start、tool_result、error、end、compact；data 使用对应已保存的 JSON 对象，不改名、改写或在分页查询时额外包装，不按查询页合并或重编号事件。
- 事件查询按 run 内 seq 升序分页，跨页覆盖全部已保留事件，包括不渲染的 start、end、compact，以及供前端展示的 error、tool_start 和 tool_result；取消收尾的模型/运行错误及工具结果同样可查，不按取消阶段或 end.seq 隐藏记录。分页不改写事件内容、编号或调用关联，单条 event 无论体积大小均完整返回已持久化的原始内容，第一版不增加响应字节上限或事件拆分协议。limit 只限制事件条数，不保证响应字节大小；模型上下文中的工具结果裁剪不影响 HTTP 查询或 SSE 返回的原始事件。不从 model_call 额外复制初始摘要正文或独立任务的 usage 统计。continuation 的 thinking/text 仍返回原始增量事件，前端按续写规则归并；截断 tool 的解析错误仍通过对应 tool_result 反馈。
- wait_for_cancel 查询立即返回当前已保存的展示内容及 status=cancelled，不等待正在执行的工具完成；后台继续保存结果，该 run 尚无取消 end。HTTP 的 cancelled 是受控投影，不能替代内部终态判断；既有或重连 SSE 仍可等待真正取消完成后的唯一 end。
- 前端订阅 SSE 收到 204 后，查询 run 概况并按需分页读取缺少的事件，不再尝试回放终态 run 的历史 SSE；相关 HTTP 返回结构变更仍须先通过 Apifox，再继续前端开发。
- 查询 failed 的 run 时，有已保存的正文输出则返回该内容及 error；没有正文输出则 output 为 null，仍返回失败状态与 error。
- 不再沿用旧 HTTP 契约中 failed.output 必须为 null 的限制；此为已确认的目标设计，现有 Go/Flutter 契约尚未修改。
- model_call 是否存在输出与 run 查询的正文投影分别处理，不能因没有正文而删除调用事实。

## 模型服务配置与密钥

- 第一版支持配置模型服务的 provider、model 与 base_url，以 Chat Completions 协议适配真实服务；核心 Agent 仍只依赖模型调用端口。
- 模型的非密钥配置保存到现有 config.yaml，顶层使用 chat_model，按 provider 分组；每组共享 base_url，并可包含多个 model。工具定义直接写在 tool 模块代码中，不从配置文件读取。

```yaml
chat_model:
  - provider: "provider1"
    base_url: "https://provider1.example"
    models:
      - model: "model1"
        context_window: 131072
        max_output_tokens: 8192
      - model: "model2"
        context_window: 131072
        max_output_tokens: 8192
  - provider: "provider2"
    base_url: "https://provider2.example"
    models:
      - model: "model1"
        context_window: 131072
        max_output_tokens: 8192
      - model: "model2"
        context_window: 131072
        max_output_tokens: 8192
```

- models 中每项增加必填 context_window 与可选 max_output_tokens；context_window 的示例值 131072 不代表所有模型的真实容量，max_output_tokens 的默认值为 8192。
- 校验 0 < max_output_tokens < context_window；上下文窗口缺失或预算非法时，仅将该模型标记为不可用，不影响其他有效模型或日程、提醒业务。
- API Key 单独保存在 .env，变量名按 `{PROVIDER}_{MODEL}` 规则定位，不将密钥值写入 config.yaml。
- .env 与 config.yaml 放在当前实例的同一数据目录，按 --data-dir 解析固定绝对路径，不依赖进程工作目录。
- 真实 .env 严禁被 Git 追踪；仓库使用 example.env 提供无真实密钥的参考格式。现有忽略规则已覆盖 .env，当前无被追踪的 .env。
- provider 与 model 在构造密钥名时转为大写，非 ASCII 字母数字字符替换为下划线；例如 deepseek/deepseek-v4-flash 对应 DEEPSEEK_DEEPSEEK_V4_FLASH。
- 该转换只用于密钥查找，不改变实际模型请求使用的配置原值；规范化后发生名称冲突时，配置校验报错。
- Go 负责读取、校验与使用配置；设置界面沿用 Flutter 编辑配置的职责，尚未实现相应配置读写与模型接入。
- AI 服务正常启动，不将 API Key 存在性作为启动成功条件；.env 不存在、所有 Key 缺失或没有本地可用模型时，也不因此使 AI 服务或其他服务启动失败。真正创建新 run 时，按所选 provider + model 检查当前模型配置及对应 Key 是否存在且非空。可用性按模型隔离：B 缺少 Key 时仅 B 不可用于新建 run，不影响 A 或已有本地数据查询。
- 创建前的 Key 检查仅确认存在且非空，不向上游发送验证请求，不判断密钥是否有效、过期或具有调用权限；这些只能在实际模型请求发出后根据上游响应获知。Key 存在不等于鉴权成功，GET /v1/model 的 available 也不表示上游有效性验证通过。
- 已配置模型的 Key 缺失、为空或本地模型预算配置无效等已知本地不可用情形，AI 创建入口返回 HTTP 503，错误为 {error: {code: "model_config_unavailable", message: "所选模型的本地配置不可用"}}，不创建 run、不占用并发名额、不记录创建幂等键。密钥已存在但真实模型请求鉴权失败时，该请求所属 run 已创建，按既定不可重试模型错误将其结束为 failed，保存并反馈实际鉴权错误；不追溯改写为创建被拒绝或本地 Key 缺失。
- 全部模型本地不可用时，既有 session、run、events 和模型列表查询仍可用，只拒绝真正的新建 run 请求；查询依赖本地数据，不要求模型调用能力已经启动。其他既有本地操作继续遵循各自契约，不因 Key 缺失统一关闭聊天 HTTP 路由；已有请求的幂等重放仍是读取原回执，不作为新建 run 拒绝。
- 已持久化 run 的重启恢复需要请求模型时，若原 provider/model 配置或 API Key 缺失，则使用 model_config_unavailable 结束为 failed，保留已有内容且不消耗网络重试；其终态提交与占用释放遵循既定流程，不与新建请求的“不创建 run”规则混淆。
- .env 支持热更新，无需重启应用；变量新增或修改后，重新检查本地配置及 Key 是否存在，后续模型请求使用最新密钥，已发出的请求继续处理；热更新同样不向上游验证 Key 有效性。
- .env 删除后，真正的新建 run 请求因缺少 Key 被配置门禁拒绝，AI 服务及本地查询仍正常提供；重新补齐所需密钥后允许新建请求，无须重启 AI 服务，不自动复活已进入终态的 run，也不将本地重新可用表述为 Key 已通过上游验证。
- 密钥不写入 run；模型请求重试仍属于原调用，但发出请求时使用当前已加载密钥。run 不保存运行参数快照，model_call.input 仍按既定规则保留实际模型请求记录。
- provider 分组结构与 models 列表字段已确定；不同 provider 可以包含同名模型，以 provider + model 组合定位配置及密钥。
- 前端通过 GET /v1/model 获取模型选项及本地配置可用性，不读取或接收 API Key。

### 创建 Run 的模型定位

- POST /v1/run 在原 session_id、input、model 基础上增加必填 provider；请求字段为 session_id、input、provider、model。
- 后端按 provider + model 校验配置并查找对应密钥；base_url 与 API Key 由后端读取，不由创建请求提供。
- 对真正的新建请求，provider + model 组合不在当前配置中时，返回 HTTP 400、error.code=invalid_request，message 说明所选模型组合未配置；组合已配置但 Key 缺失、为空或本地预算无效时，返回 HTTP 503、error.code=model_config_unavailable。两类拒绝均不创建 run、不占用活跃名额、不登记该次创建幂等键；已有请求的幂等重放仍先读取原回执，不因当前配置变化改写或重建原 run。
- 同一 session 内，创建请求的幂等判断只使用幂等键与 input；provider、model 不参与 input 的幂等比较。更换 provider 或 model 不改变已创建的 run。
- 同一幂等键与相同 input 返回原 run，不重复启动，也不改写首个 run 的模型选择及运行参数；同键不同 input 返回幂等冲突。
- 原 run 已终态时，同键重试仍返回原 run_id，并返回已保存的 output 或 error，作为原创建请求的回执，不创建新 run。
- context_budget_exceeded 的创建前预算失败同样产生可查询、可幂等重放的 failed run；先按既定规则识别幂等重放，再对真正的新请求检查预算。没有 checkpoint 仅表示未进入模型调用，不能因该失败 run 缺少 checkpoint 而在重启后执行它。
- 同一 session 最多一个活跃 run；正常运行中的 running 与 wait_for_cancel 都占用名额。持久化故障且三次收尾重试耗尽后，原 run 按异常释放规则不再占用当前进程名额，即使数据库尚保留旧状态。已有活跃 run 时，不接受创建新的 run；幂等重放只返回原记录，不属于新建。
- 活跃 run 准入由聊天服务的内存原子计数控制，不为 session_id 添加覆盖 running/wait_for_cancel 的条件唯一索引；同会话占用和既有全局上限按统一准入流程协调，幂等重放不重复计数。
- 创建新 run 时原子预留名额，持久化创建失败则回退预留；正常路径进入 succeeded、failed、cancelled 并持久化成功后释放名额，wait_for_cancel 或父 session 隐藏均不提前释放。持久化故障且三次失败收尾重试耗尽时，按异常规则记录 error 日志后释放，不要求终态落库。各生命周期分支须保证只结算一次。
- 启动时先按同会话后续 run 的存在性识别旧 running run，将其登记为失败收尾而非业务恢复；再结合持久化的 running/wait_for_cancel 状态及收尾进度重建内存占用，之后允许新的 run 准入。旧 run 的占用按失败提交成功或三次收尾重试耗尽规则结算；尚未完成收尾的隐藏 session 所属 run 也计入，避免重启后将计数清零而突破既有上限。配置加载、数据库连接及未完成 run 的恢复登记和占用重建完成后，HTTP 服务即可进入 ready，不等待旧 run 全部执行完毕；后台只对允许恢复的最新 run 推进业务，其余执行既定失败或取消收尾。已有实际占用的 session 不接受新 run，其他 session 按剩余全局并发名额正常准入，幂等重放沿用原规则。
- wait_for_cancel 期间，新键提交的新消息仍返回 session_busy；旧键与相同 input 的幂等重放复用原 run 的回执，对外 status=cancelled，不创建新 run，也不因对外状态释放内部占用。
- 同一 session 没有活跃 run 时，相同 input 可以使用新的幂等键创建新 run；上一 run 已进入终态后，用户新一轮发送必须使用新键。
- 新一轮用户提交与旧请求重试分别处理：新一轮必须用新键，旧键用于取得原 run 的回执。
- 幂等键在 session 内唯一，数据库使用 `(session_id, idempotency_key)` 唯一约束；不同 session 可以独立使用同一个键。
- session_id 用于定位提交所属边界，同键是否冲突仅比较 input；provider、model 不参与 input 的比较。
- 不再按固定 deepseek-flash 名称识别所有合法模型，合法组合由已加载配置提供；具体模型定位与可用性通过服务端口校验。
- 以上为已确认的目标契约，现有 Go Handler、聊天服务契约及 Flutter 请求尚未修改；实现 HTTP 变更后须先完成 Apifox 测试，再继续对应前端开发。
- 创建服务端口返回包含 run_id、status、output、error 的受控创建回执，不能继续仅返回字符串 ID。
- 新建 run 与重放 running run 返回 HTTP 202，重放真正终态或 wait_for_cancel run 的取消投影返回 HTTP 200。各分支统一返回 run_id/status/output/error 四个字段，wait_for_cancel 对外 status=cancelled，内部等待期间仍占用活跃名额。
- 对外 running 回执的 output、error 均为 null；真正终态或 wait_for_cancel 的取消投影返回当前已保存的值，没有正文时 output 为 JSON null。工具收尾错误保存于原 tool_call.error 并通过对应 tool_result 反馈，不将其误报为整个 run 失败；取消投影不表示已生成 end，SSE 仍须等真正取消完成。
- 终态重放可由前端直接展示已有结果，无须再建立 SSE；具体 DTO 实现尚未落地。

### 模型列表查询

- 新增 GET /v1/model，响应为 models 数组，各项包含 provider、model、available。
- available 仅表示本地模型配置与对应密钥是否齐全，不表示已经完成上游服务探测。
- 返回全部配置的模型选项；缺少或为空的对应密钥、或本地预算配置无效时，该模型 available 为 false，不影响其他模型的 available。配置没有模型时 models 为 []；即使所有模型 available=false，AI 服务仍正常启动且查询可用。available 仅为只读的本地配置状态，不作为服务启动门槛，也不表示 Key 已通过上游验证。
- 前端按 provider + model 选择模型，创建 run 时后端仍按当前配置重新校验；密钥不通过该接口返回。
- 此接口为目标契约，尚未实现；须先通过 Apifox 验收，再进行对应前端开发。

## 工具定义与注册

- 工具定义直接内置在代码中，由独立 tool 模块管理名称、描述、参数 JSON schema、dangerous、并行/串行标记及对应执行入口；不新增或读取额外工具配置文件。
- 工具名称、描述、参数 schema、dangerous 和并行/串行标记等定义元数据不从配置文件读取；修改定义须修改代码、重新构建并重启应用。工具运行参数使用通用 config 加载流程，不由 tool 模块另建文件读取器或监听器。
- 删除此前独立工具文件的缺失、校验失败、AI 不可用及已有 run 等待修复文件后恢复的规则。代码内置定义不依赖实例目录中的工具文件；模型配置及密钥的既有可用性检查保持其各自职责。
- 工具相关运行参数与正常配置一起加载：internal/config/default.yaml 为默认基线，实例 config.yaml 为用户配置，通过既有 config 模块统一合并并发布有效配置，不新增工具专用文件。默认每 run 并发 3、单次超时 30 秒、首次执行外最多重试 3 次。max_retry 创建逻辑调用时保存，单次超时每次尝试前读取并在该尝试内固定，并发上限在调度时读取最新值。工具模块通过装配提供的配置读取契约取得所需参数；具体配置键及有效范围继续细化，不新增 run 快照字段。
- 当前默认危险工具仅删除日程；创建、修改和查询工具不分配工具业务幂等键。
- Agent 循环通过 tool 模块的窄接口取得内置定义及执行能力；工具模块通过注入的业务端口执行日程操作，HTTP Handler 不管理工具注册或执行。

## 服务与装配边界

| 部分 | 职责 |
|---|---|
| HTTP Handler | 请求校验、响应转换和 SSE 传输，通过聊天服务契约调用业务。 |
| `chat.Service` | run/session 用例、并发准入、取消、查询和事件订阅。 |
| Agent loop | 执行单个 run，管理 checkpoint、模型调用、工具队列和重试；可先作为聊天模块内部执行器。 |
| tool 模块 | 管理代码内置的工具定义、注册及执行入口，提供参数校验和工具执行能力，不读取额外工具定义文件。 |
| App | 管理整体启动、恢复和退出，提供日程正式业务操作。 |
| 存储与模型适配器 | 分别实现持久化端口和模型调用端口。 |

- 工具通过注入的窄业务接口调用 App 的正式日程操作；Agent 不直接依赖具体 App 类型，不绕过业务操作写日程表。
- 工具采用现有 Go 业务函数的适配封装，复用已有校验、事务、提醒更新及调度通知逻辑；工具层负责 schema 校验、JSON 参数到函数参数的转换、结果序列化与执行调度，不复制业务实现。
- context、幂等键及运行身份等执行信息由后端注入函数调用，不作为模型可自行填写的工具业务参数。
- App 已依赖聊天契约，Agent 不反向导入 App，避免包依赖环；入口负责连接接口与具体实现。
- 聊天服务与执行器通过窄端口访问存储及模型，不依赖 HTTP Handler 或具体 SQLite、模型供应商实现。
- 日程操作复用现有规则校验、幂等写入与调度通知边界；模型请求、工具退避等长等待不占用日程正式操作锁。
- 生命周期方法、具体端口和文件划分尚待确定。

### 正常退出与重启

- 正常退出应用或停止 Go 服务时，停止接收新任务、停止启动后续步骤，并保存已经完成的执行结果。
- 收到正常退出信号时，立即关闭正在进行的模型 HTTP 请求或流连接，不等待模型生成完整回复；在既有退出预算内立即保存此前实际接收的正文、thinking、工具参数片段、事件尾批及已取得的响应标识和 usage。断开后不再接纳迟到增量，不执行尚未完整接纳的工具请求。
- 仅因宿主正常退出而中断的模型调用保持未完成事实，run 保持 running，model_call 保持 running，checkpoint 留在本次普通调用的 model_call_start 或摘要调用的 compact；保存实际部分 output，但不推进为 model_call_end，不将退出关闭错误记为用户取消、模型失败或 run 终态，不生成该 run 的 end。已经完整接收并提交的成功结果保持原事实，不因退出回退。
- 下次启动按同一逻辑 model_call、原 input 和 checkpoint 恢复未完成请求，沿用既定重发及计数规则，不将保存的部分 output 误判为完整回复；旧展示事件仍保留，不能据部分工具参数补执业务。正常退出引起的连接中断与实际持久化故障分别处理，后者仍遵循失败收尾及 error 日志规则。
- 未完成的正常 run 保留 `running` 状态与 checkpoint，下次启动按既定恢复规则继续；工具定义由代码提供，不设置等待外部工具配置文件修复的分支。正常退出不等同于用户取消 run。
- 已处于 wait_for_cancel 的 run 保留取消状态，下次启动单独继续只读取消收尾，不恢复正常业务推进。
- 退出收尾须适配现有宿主共享的 5 秒退出预算，不能假设可以无限等待工具或模型完成；预算内未完成的工作按持久化进度在重启后处理。

## 表结构与服务契约的落地草案

以下将已确认行为映射为结构候选，供后续逐项核对；不是迁移脚本，也不表示数据库结构已经变更。字段名及内部元数据可在实现前继续收敛。

### 六张 AI 表的职责与字段

六张物理表统一使用 ai_ 前缀和复数形式，业务叙述仍使用原逻辑名称。

| 逻辑表 | 物理表名 | 字段与归属 |
|---|---|---|
| session | ai_sessions | 自增 id 主键、业务字段 session_id、session_name、可空 INTEGER 引用 compact_event_id、创建及更新时间、删除标记。session_id 不承担主键，使用 UNIQUE 保证业务 ID 唯一并支持外键引用。compact_event_id 仅指向本 session 最新成功的 compact，完整摘要生效时才原子更新；摘要调用关联及 run/seq 分界从事件取得，不重复存储。 |
| run | ai_runs | 自增 id 主键、TEXT 业务字段 run_id、引用 ai_sessions.session_id 的 TEXT session_id、用户 input、内部 status、最后一轮面向用户并包含其续写拼接结果的 output、error、创建幂等键、模型选择及时间元数据。业务 run_id 不承担主键，使用 UNIQUE 保证业务 ID 唯一并支持外键引用。不新增 config_snapshot 或运行配置列，不要求创建时冻结配置并在恢复时沿用。完整事件不内嵌到 run。 |
| model_call | ai_model_calls | 自增 id、run_id、session_id、实际 input/output、可空模型回复 id、调用 status、续写布尔标记 continuation（默认 false，续写为 true）、error、retry_count、max_retry、next_retry_at、retry_history 及时间元数据。不增加独立业务调用 ID、purpose 改造、continuation_of 或指向 event 的外键。 |
| checkpoint | ai_checkpoints | run_id 直接作为主键并关联 ai_runs.run_id，不另设自增 id；每个 run 最多一行，首次模型调用尚未发生时允许无记录。model_call_id 引用 ai_model_calls.id，发请求前与调用 input、running 及执行阶段原子保存，回复完成后再用短事务保存剩余结果与进度。status 记录 compact、model_call_start、model_call_end、tool_start、tool_end；摘要及其续写保持 compact，完整成功且普通输入在预算内时原子切换下一普通调用的 model_call_start，普通调用开始/结束使用 model_call_start/model_call_end；tool_start 每个工具完成后持续更新，全部工具完成才写 tool_end。state 使用 JSON，结构为 {context: array, completed_tool: array, last_tool: str}；context 保存完整消息数组，completed_tool 仅保存当前 model_call 已完成的 tool_call_ids，新调用开始时清空，恢复同一次调用保留；last_tool 初始为 ""，有工具记录后使用 tool_use: tool_name-tool_id 或 tool_result: tool_name-tool_id。覆盖更新，不保留链。 |
| event | ai_events | 数据库自增主键 id（不表示顺序）、run/session 关联、模型调用相关事件使用 model_call_id 引用 model_call.id、适用时保存模型回复 id、事件类型、完整事件数据、run 内顺序及 SSE 编号 seq、记录时间；添加 UNIQUE (run_id, seq)。compact 额外使用内部布尔字段 compact_succeeded，仅表示摘要是否成功生效，其他事件该字段为空。当前已定义事件均按 run 连续编号，包括被截断的 tool 原始记录。 |
| tool_call | ai_tool_calls | 本地记录主键、run_id、session_id、所属模型调用关联、模型 tool_call_id、name、以 JSON 字符串编码保存实际 arguments 原文的 input、output、error、status、可空 TEXT idempotency_key、retry_count、max_retry、next_retry_at 及执行时间元数据；不保存队列次序。max_retry 在创建逻辑调用时从当前配置保存，同次重试及恢复固定。dangerous=true 保存幂等键，截断或解析、校验失败也保留，其他工具为 SQL NULL；使用普通 UNIQUE (run_id, idempotency_key)。被截断的调用同样创建记录并保存 input/error，最终 failed。 |

- 已确认的创建唯一约束为 (session_id, idempotency_key)；内部 running 与 wait_for_cancel 共同参与活跃 run 的准入判断，不能使用对外 cancelled 投影判断是否释放名额。
- 同 session 活跃 run 的互斥不增加数据库唯一索引，使用内存原子准入计数；重启从持久化状态重建占用。创建请求的幂等唯一约束保持不变。
- checkpoint 以 run_id 为主键，覆盖更新且不另设自增 id；event.id 仅为数据库自增主键，seq 才是 run 内事件顺序，添加 UNIQUE (run_id, seq)；同一 run 的 end 只生成一次。
- tool_call 的本地主键与模型 tool_call_id 分开；关联 model_call 时引用其已有自增主键，不生成新的业务 ID。
- 不为 tool_call_id 添加单列唯一约束，也不添加 (model_call_id, tool_call_id) 联合唯一约束；这不改变创建 run 的幂等唯一约束及已有日程业务回执的去重规则。
- 无数据库唯一约束不表示允许接纳同一模型回复内的重复 tool_call_id；重复 ID 按前述模型回复接纳规则拒绝，不进入工具调度。跨 model_call 的同值 ID 仍分别记录，checkpoint 的 completed_tool 随新 model_call 清空。
- 工具幂等键仅用于 dangerous=true，包含参数校验失败的调用；其他工具为 SQL NULL，使用普通 UNIQUE (run_id, idempotency_key)，不采用哨兵默认值或部分唯一索引。
- 不增加工具队列排序字段或重入队事件，不约定 FIFO 或恢复原队列顺序；调用状态、退避时间、并发上限及串行标记决定是否可执行，event.seq 只负责事件内容顺序。
- 表间关联及检查约束须落实 run/session 所属关系；model_call.input/output、event.data、retry_history 统一使用 JSON 列及 json_valid 检查，可空列允许 SQL NULL。run、model_call、tool_call 的 error 使用允许 SQL NULL 的 JSON 列，内容统一为 {code, message}，添加 CHECK (error IS NULL OR json_valid(error))。tool_call.input 同样使用 JSON 列及 CHECK (json_valid(input))，内容是原始 arguments 的外层 JSON 字符串编码，不要求解码后的参数原文是合法 JSON；主键及五个唯一约束的范围已确定，其余具体检查约束与迁移 SQL 仍在实施前细化。

### 已确认的枚举 CHECK 约束

以下五个字段均为 TEXT NOT NULL，并增加对应 CHECK；取值之外的内容由数据库拒绝。CHECK 只约束可保存的值，不负责判断前后状态转换是否合法，转换仍由服务控制。本节只确认设计，不执行建表或迁移。

| 表与字段 | CHECK 表达式 |
|---|---|
| ai_runs.status | CHECK (status IN ('running', 'wait_for_cancel', 'succeeded', 'failed', 'cancelled')) |
| ai_model_calls.status | CHECK (status IN ('running', 'retry_wait', 'succeeded', 'failed', 'cancelled')) |
| ai_tool_calls.status | CHECK (status IN ('running', 'retry_wait', 'succeeded', 'failed', 'skipped')) |
| ai_checkpoints.status | CHECK (status IN ('compact', 'model_call_start', 'model_call_end', 'tool_start', 'tool_end')) |
| ai_events.type | CHECK (type IN ('start', 'thinking', 'text', 'tool_start', 'tool_result', 'error', 'end', 'compact')) |

工具状态不包含 pending，摘要事件类型使用小写 compact；不因同名 checkpoint 阶段而混用两个字段的约束。

### 已确认的数值与布尔 CHECK 约束

| 表与字段 | CHECK 表达式 |
|---|---|
| ai_sessions.deleted | CHECK (deleted IN (0, 1)) |
| ai_model_calls.continuation | CHECK (continuation IN (0, 1)) |
| ai_model_calls.max_retry、ai_tool_calls.max_retry | 各自添加 CHECK (max_retry >= 0) |
| ai_model_calls.retry_count、ai_tool_calls.retry_count | 各自添加 CHECK (retry_count >= 0 AND retry_count <= max_retry) |
| ai_events.seq | CHECK (seq >= 1) |

以上字段保留各自的 NOT NULL 及既定默认值。seq 的连续分配仍由 run 协调器和事务保证，CHECK 不替代既有 UNIQUE (run_id, seq)。ai_events.compact_succeeded 不增加 CHECK，包括不增加与 type 联动的取值检查；其原有 compact 事件为 0/1、其他事件为 SQL NULL 的记录规则由服务保证。

### 已确认的主键与索引范围

暂不额外增加普通查询索引，删除此前五个普通索引候选；除各表主键外，共保留以下五个已确认唯一约束，其中两个业务 ID 单列唯一约束支持 run/session 外键引用：

| 表 | 唯一约束 |
|---|---|
| ai_sessions | (session_id) |
| ai_runs | (run_id) |
| ai_runs | (session_id, idempotency_key) |
| ai_events | (run_id, seq) |
| ai_tool_calls | (run_id, idempotency_key) |

ai_sessions 与 ai_runs 均使用 id INTEGER PRIMARY KEY AUTOINCREMENT，主表中的 session_id、run_id 分别为 TEXT NOT NULL UNIQUE 业务字段，不承担主键。所有 run/session 关联均使用对应业务 ID；checkpoint.run_id 使用 TEXT NOT NULL PRIMARY KEY 关联 ai_runs.run_id，不另设自增 id。业务 ID 的两个单列唯一约束与原有三个联合唯一约束共同构成上述五个约束，不额外增加普通查询索引。

所有关联 run/session 的数据库列使用 TEXT NOT NULL，run_id 引用 ai_runs.run_id，session_id 引用 ai_sessions.session_id；ai_runs 自身的 session_id 同样引用 ai_sessions.session_id。三个既定唯一约束的列组合不变，所涉 run/session 分组值均使用业务 ID。model_call_id 仍引用 ai_model_calls.id，compact_event_id 仍引用 ai_events.id；本次调整不改变这些关联。事件顺序仍只由 seq 表达，自增 id 只作行身份。

所有 AI 表外键均不启用级联删除。服务对外仅提供 session 软删除接口，不提供这些记录的物理删除接口；软删除只更新 session 的删除标记及对应更新时间，不连带删除或为所属 run、model_call、checkpoint、event、tool_call 设置删除标记。所属记录完整保留，对外可见性沿用父 session 的软删除规则。被引用的父记录不得在仍有引用时物理删除，不通过自动清空外键解除关联。

HTTP 路径、请求、响应及数据库 run/session 关联统一使用业务 run_id/session_id 字符串，无须为了这些关联转换为自增主键；子表不重复保存对应的整数 run/session 引用。相关父键要求见 [SQLite Foreign Key Support](https://www.sqlite.org/foreignkeys.html#required_and_suggested_database_indexes)。本节仅调整设计文档，不执行数据库变更。

### 列级草案

以下整理已确认字段及其类型、空值和关联。时间列沿用现有存储的 Unix 纳秒 INTEGER；模型回复 ID 暂名 model_reply_id，时间列命名和未单独讨论的默认值属于待核对的工程草案。JSON 列均检查 json_valid，可空列允许 SQL NULL；不会因本节整理而创建表或执行迁移。

#### ai_sessions

| 列 | 类型及空值 | 含义与约束 |
|---|---|---|
| id | INTEGER PRIMARY KEY AUTOINCREMENT | 本地数据库自增主键。 |
| session_id | TEXT NOT NULL UNIQUE | 会话业务标识，不承担主键；保证业务 ID 唯一并作为外键引用目标。 |
| session_name | TEXT NOT NULL，默认 '' | 会话名称。 |
| compact_event_id | INTEGER，可空 | 引用 ai_events.id；仅指向本会话最新成功的 compact。 |
| deleted | INTEGER NOT NULL，默认 0 | 软删除标记，CHECK (deleted IN (0, 1))；延续现有会话删除后其 run 不可见的契约。 |
| created_at | INTEGER NOT NULL | 创建时间。 |
| updated_at | INTEGER NOT NULL | 更新时间。 |

#### ai_runs

| 列 | 类型及空值 | 含义与约束 |
|---|---|---|
| id | INTEGER PRIMARY KEY AUTOINCREMENT | 本地数据库自增主键。 |
| run_id | TEXT NOT NULL UNIQUE | 一次用户请求的业务标识，不承担主键；保证业务 ID 唯一并作为外键引用目标。 |
| session_id | TEXT NOT NULL | 引用 ai_sessions.session_id，使用业务会话标识。 |
| provider | TEXT NOT NULL | 创建时选择的模型提供方。 |
| model | TEXT NOT NULL | 创建时选择的模型。 |
| input | TEXT NOT NULL | 本轮真实用户输入。 |
| status | TEXT NOT NULL | CHECK 限定为 running、wait_for_cancel、succeeded、failed、cancelled。 |
| output | TEXT，可空 | 最后一轮正文，包含其续写拼接结果。 |
| error | JSON，可空 | 最终错误 {code, message}。 |
| idempotency_key | TEXT NOT NULL | 创建 run 的请求幂等键；UNIQUE (session_id, idempotency_key)。 |
| created_at | INTEGER NOT NULL | 创建时间；会话内 run 按现有创建时间顺序读取，不作为相对时间解析基准。 |
| finished_at | INTEGER，可空 | 内部终态完成时间；wait_for_cancel 尚未完成收尾。 |

#### ai_model_calls

| 列 | 类型及空值 | 含义与约束 |
|---|---|---|
| id | INTEGER，自增主键 | 本地逻辑模型调用身份。 |
| run_id | TEXT NOT NULL | 引用 ai_runs.run_id。 |
| session_id | TEXT NOT NULL | 引用 ai_sessions.session_id，与所属 run 的 session_id 一致。 |
| input | JSON NOT NULL | 实际请求，包含完整 messages、工具定义及生成参数。 |
| output | JSON，可空 | 实际响应或已收到的部分内容，含原始 usage。 |
| usage | JSON，可空 | 模型实际返回的原始 usage，供统计读取；未返回时为 SQL NULL，CHECK (usage IS NULL OR json_valid(usage))，不填零值或估算值。 |
| model_reply_id | TEXT，可空 | 供应商回复 ID，不作为本地关联主键。 |
| status | TEXT NOT NULL | CHECK 限定为 running、retry_wait、succeeded、failed、cancelled。 |
| continuation | INTEGER NOT NULL，默认 0 | CHECK (continuation IN (0, 1))，标记是否续写。 |
| error | JSON，可空 | 最终错误 {code, message}。 |
| retry_count | INTEGER NOT NULL，默认 0 | 已使用的重试次数，不含首次请求及额外的生命周期恢复；CHECK (retry_count >= 0 AND retry_count <= max_retry)。 |
| max_retry | INTEGER NOT NULL | 本逻辑调用登记时保存的重试上限，未配置时使用 3；CHECK (max_retry >= 0)。 |
| next_retry_at | INTEGER，可空 | retry_wait 的下次可重试时间。 |
| retry_history | JSON NOT NULL，默认 '[]' | 本逻辑调用的重试明细数组。 |
| created_at | INTEGER NOT NULL | 逻辑调用登记时间，不用于决定内容顺序。 |
| finished_at | INTEGER，可空 | 逻辑调用终态完成时间。 |

#### ai_checkpoints

| 列 | 类型及空值 | 含义与约束 |
|---|---|---|
| run_id | TEXT NOT NULL PRIMARY KEY | 关联具有 UNIQUE 约束的 ai_runs.run_id，每个 run 最多一行，使用已有业务 run_id，不独立生成。 |
| model_call_id | INTEGER NOT NULL | 引用当前 ai_model_calls.id。 |
| status | TEXT NOT NULL | CHECK 限定为 compact、model_call_start、model_call_end、tool_start、tool_end。 |
| state | JSON NOT NULL | 固定为 {context: array, completed_tool: array, last_tool: str}。 |

首次模型调用登记前允许没有 checkpoint 行。新调用开始时重置 completed_tool 和 last_tool，同一次调用恢复时保留；不增加占位状态、快照链或独立工具结果列。

#### ai_events

| 列 | 类型及空值 | 含义与约束 |
|---|---|---|
| id | INTEGER，自增主键 | 数据库身份，不表示事件顺序。 |
| run_id | TEXT NOT NULL | 引用 ai_runs.run_id。 |
| session_id | TEXT NOT NULL | 引用 ai_sessions.session_id，与所属 run 的 session_id 一致。 |
| model_call_id | INTEGER，可空 | 模型相关事件引用 ai_model_calls.id；其他生命周期事件可空。 |
| model_reply_id | TEXT，可空 | 适用时记录产生该事件的供应商回复 ID。 |
| type | TEXT NOT NULL | CHECK 限定为 start、thinking、text、tool_start、tool_result、error、end、compact。 |
| data | JSON NOT NULL | start、compact 为 {}；thinking/text 保存 content；tool_start 保存原 function 数组；tool_result 固定保存 {tool_call_id, output, error}，原调用 ID 与对应 function[].id 严格一致，output 保留工具原始 JSON 值（没有则为 null），成功 error=null，最终失败 error={code, message}，取消跳过使用 error.code=tool_skipped 且照常发送前端；end 只保存 status；error 事件仅保存模型或 run 错误的 {code, message}，不承载工具错误。argument 为解析后的 JSON object，截断、JSON 语法非法或顶层非对象时为 JSON null，事件不保留原始片段。 |
| seq | INTEGER NOT NULL | run 内从 1 开始的连续事件顺序及 SSE 编号；CHECK (seq >= 1)，UNIQUE (run_id, seq)。 |
| compact_succeeded | INTEGER，可空 | compact 必填 0/1，初始 0，完整摘要生效后为 1；其他事件为 SQL NULL；由服务保证，不增加 CHECK。 |
| created_at | INTEGER NOT NULL | 记录时间，不替代 seq 排序。 |

compact 必须引用原始摘要调用，摘要续写不会改变该关联；thinking/text 续写新建事件，保留各自 model_call_id。截断 tool 的事件以 argument=null 保留并展示，不保存截断参数片段。

#### ai_tool_calls

| 列 | 类型及空值 | 含义与约束 |
|---|---|---|
| id | INTEGER，自增主键 | 本地工具执行记录身份。 |
| run_id | TEXT NOT NULL | 引用 ai_runs.run_id。 |
| session_id | TEXT NOT NULL | 引用 ai_sessions.session_id，与所属 run 的 session_id 一致。 |
| model_call_id | INTEGER NOT NULL | 引用产生调用的 ai_model_calls.id。 |
| tool_call_id | TEXT NOT NULL | 模型返回的调用标识，参数被截断时仍有效，不添加唯一约束。 |
| name | TEXT NOT NULL | 工具名称。 |
| input | JSON NOT NULL | 实际收到的 arguments 原文经外层 JSON 字符串编码后的值，包含截断或非法参数；解码后再解析校验。 |
| output | JSON，可空 | 工具实际结果，失败详情由 error 保存。 |
| error | JSON，可空 | 最终错误 {code, message}。 |
| status | TEXT NOT NULL | CHECK 限定为 running、retry_wait、succeeded、failed、skipped。 |
| idempotency_key | TEXT，可空 | 仅 dangerous=true 生成并保存；其他工具为 SQL NULL；UNIQUE (run_id, idempotency_key)。 |
| retry_count | INTEGER NOT NULL，默认 0 | 已用工具重试次数，包含结果未确认后的恢复重执行；首次执行不计，跨重启保留；CHECK (retry_count >= 0 AND retry_count <= max_retry)。 |
| max_retry | INTEGER NOT NULL | 创建本次逻辑调用时读取并保存的最大重试次数，未配置时使用 3；同次重试及重启恢复固定，0 表示不重试；CHECK (max_retry >= 0)。 |
| next_retry_at | INTEGER，可空 | retry_wait 的下次可重试时间。 |
| created_at | INTEGER NOT NULL | 工具调用记录的创建时间；正常路径在开始处理时建行，未开始便取消跳过时直接创建 skipped 记录。 |
| finished_at | INTEGER，可空 | 成功、最终失败或 skipped 的完成时间。 |

工具定义由代码提供，最大重试次数、超时及并发上限从现有 config.yaml 取得；max_retry 在创建工具调用时保存到本行，配置变化只影响新调用。新增该列不等同于新增 run 配置快照。截断、解析失败及 schema 校验失败均记录 tool_call，保存实际 input 和 error，最终为 failed，不执行业务且不做工具重试；原 ID 的错误 tool output 与 completed_tool 更新原子保存。已有结果在恢复时复用；创建、修改结果未确认时复用本行和参数，在本行剩余预算内再次执行并增加 retry_count，预算耗尽仍未确认结果才按重试耗尽最终失败处理。

### HTTP 服务端口

内部运行状态与 HTTP 展示状态分别建模；run/session 准入、资源释放、恢复及 SSE 终态判断只能使用内部状态，不能将对外 cancelled 投影反向写回内部运行。T01 无在途工具时可直接完成模型取消；后续扩展 wait_for_cancel 时仍沿用这一边界。

| 服务操作 | 目标受控结果 |
|---|---|
| CreateRun | 返回 run_id/status/output/error 创建回执，处理持久化幂等与准入，不由 Handler 启动循环。 |
| GetRun | 返回 run 的受控概况，不内嵌事件列表；不能把模型实际完整请求作为 run.input 返回。 |
| GetRunEvents | 独立分页读取同一 run 按 seq 升序排列的事件，保留原数据、编号及调用关联；after_seq 读取 seq 大于游标的事件，limit 限制条数；固定返回 events/next_after_seq/has_more，limit 默认 10、范围 1 至 100，非法参数返回 400 invalid_request。 |
| CancelRun | 按阶段受理取消：模型调用关闭连接并保存已接收内容；工具执行中先保存 wait_for_cancel，等待完成或超时并记录结果后才提交 cancelled 与唯一 end。HTTP 回执继续将 wait_for_cancel 投影为 cancelled，不能据此提前发 end 或释放名额。 |
| Subscribe | running、wait_for_cancel 时新订阅替换旧订阅，按自身可选游标衔接可见历史及实时事件；只有真正终态才返回终态查询信号，由 Handler 转为 204。等待取消时持续订阅至最终 end；订阅关闭或替换不影响收尾。 |
| GetModels | 从有效配置及当前密钥可用性生成 provider/model/available 列表，不做上游探测。 |
| GetSession、ListSessions、DeleteSession | 继续由聊天服务管理会话用例，HTTP 不直接访问 AI 表或操作执行器。删除接口仅软删除 session，不连带删除所属 run、模型调用、checkpoint、事件或工具调用，不提供物理删除接口；持久化实现保留原会话可见性契约。 |

T01 将现有 chat.Service 及 DTO 迁移到目标契约：创建输入和 run 概况补齐 provider，CreateRun 返回四字段回执，thinking/text 仅保留 content，模型或 run 错误使用独立 error，end 仅保留 status。旧 tool、stop_reason 和事件内嵌 error 随 Mock 契约移除；后续工具切片使用 tool_start/tool_result。这里描述迁移目标，不表示当前 contracts.go 已实现；HTTP 变化仍须先通过 Apifox 验收，再继续相应 Flutter 适配。

### 内部存储与 App 边界

- 存储端口按原子业务操作提供能力：创建 run 及其幂等记录、保存模型调用开始 checkpoint、更新模型调用与 checkpoint、追加已编号事件、保存工具结果与进度、受理取消、提交 run 终态，以及只读详情与恢复扫描。run 创建允许暂时没有 checkpoint；需要共同提交的数据仍须使用同一原子操作，不能由 Handler 或循环拼接多个独立写入冒充事务。
- run 协调器持有推进权；工具执行器只返回结果，不自行覆盖整个 checkpoint。超时与正常返回由同一协调器裁定，过期尝试不能再次结算。
- App 为工具提供窄业务端口：日程查询、创建、带版本检查的修改、带版本检查的幂等软删除、只读业务回执查询。现有 WriteSchedule 已有可选幂等键与版本参数，创建、修改工具使用空业务 key；现有 DeleteScheduleWithResult 只有日程 ID，删除的版本与幂等能力、只读回执查询仍需补齐。
- 删除的版本比较、业务变更与幂等回执必须在同一业务事务完成；不能在工具层先读版本，再调用不检查版本的删除函数来替代事务内检查。命中原幂等回执时复用原结果，不因原操作已经改变版本而拒绝恢复。
- 数据库适配器实现事务与约束；模型适配器实现协议解析、有效输出识别与取消。循环只依赖契约，不依赖 SQLite 连接或 HTTP Handler。
- App 负责调用聊天运行时的启动恢复与停止能力。先根据 run 状态及同会话后续 run 筛选：已有后续 run 的旧 running run 仅失败收尾，允许恢复的最新 running run 读取 checkpoint，wait_for_cancel 独立只读收尾；不把启动恢复变成前端必须先发起的请求。
- 启动恢复分为 ready 前的登记与 ready 后的后台推进：先完成配置加载、数据库连接、未完成 run 登记以及会话和全局名额占用重建，再发布 HTTP ready；不将旧模型请求、工具执行或取消收尾全部完成作为 ready 条件。后台恢复继续遵守已有会话互斥、并发及状态转换规则。

### 现有函数与工具适配的映射

以下为封装方向，工具名称、输入 schema 及注册代码尚未实现。

| 工具职责 | 复用入口及适配要求 |
|---|---|
| 查询单条日程 | App.GetSchedule，输入日程 ID，返回已有业务视图及 revision。 |
| 查询日程列表或日期安排 | 复用 App.ListSchedules、QueryScheduleState、QueryOccurrences 等所需入口；工具 JSON 过滤条件转换为已有查询参数，不向模型暴露 Go 函数类型或底层数据库。 |
| 创建日程 | 当前 dangerous=false，工具记录的 idempotency_key 为 NULL；可用 App.CreateSchedule 或等价 WriteSchedule 创建入口，空 key 不保存业务回执。正常恢复时，结果未确认则复用原工具记录及参数，在剩余预算内再次执行并增加 retry_count。 |
| 修改日程 | 当前 dangerous=false，通过 App.WriteSchedule 携带 expectedRevision、使用空业务 key，保留既有版本检查及调度通知。正常恢复时，结果未确认则复用原工具记录及参数，在剩余预算内再次执行并增加 retry_count；重放遇到版本冲突时按实际错误回填。 |
| 删除日程 | 复用现有软删除逻辑，提供支持 expectedRevision 与工具幂等键的正式 App 入口；版本校验、删除效果与回执同事务提交。 |
| 恢复时核对业务结果 | App 的只读幂等回执查询端口；取消恢复不得调用可能执行新业务的写入工具。 |

## 实施前仍需细化的事项

核心执行、恢复、取消、幂等、上下文分界及事件可见性规则已确认。以下是尚未定稿的落地规格，不代表重新开放前文已经确认的行为。

- 数据结构：核对其余列级草案、检查约束及迁移方案；ai_sessions、ai_runs 使用自增 id 主键，所有 run/session 关联统一使用 TEXT 业务 ID，接口保持业务字符串 ID。ai_runs.run_id、ai_sessions.session_id 分别具有单列唯一约束，与原有三个联合唯一约束合计五个，暂不额外增加查询索引。checkpoint 各阶段的具体 JSON 及摘要到普通调用切换的恢复示例继续细化。摘要覆盖范围、摘要正文拼接、input/output 恢复入口、事件批量保存、seq 顺序、compact 引用、续写调用及截断工具失败记录等规则已确定，不重开 FIFO 或计数字段设计。
- 数据契约：其余尚未覆盖的错误映射。独立 run 概况与 events 分页查询的 DTO、分页参数及原始事件完整返回规则已确定，不再列为待定。tool_start 的原 function 数组、tool_result 固定的 {tool_call_id, output, error}、成功 error=null 与失败 error={code, message}、原始 output 保留及没有输出时为 null、工具错误不重复发 error、严格调用 ID 配对及单个/多个工具折叠均已确定；取消跳过的工具同样保存 tool_result、分配 seq 并发送前端，error.code=tool_skipped，模型上下文保留原调用并按原 ID 回填 content="aborted" 的 tool 消息，与 completed_tool/last_tool 及 checkpoint 原子保存。实时和查询回放使用持久化的同一结果事件。协作式取消须保存已接收模型内容及工具 output/error，只有内部 cancelled 持久化后才发送唯一 end，HTTP 仍将 wait_for_cancel 显示为 cancelled。模型/运行错误及工具结果提交后均按 seq 推送和查询返回，取消收尾记录不隐藏，前端事件序号保持连续；不新增内部事件可见性字段。end 只保留 status、模型 finish_reason 保留在原 model_call.output、模型/运行 error 的 {code, message}、thinking/text 的 content、tool_start 的 function 数组及参数错误表示、已确认错误码等规则不再列为待定。
- 上下文组装：事件增量到模型结构化消息的组装、其余部分回复的具体消息形式、各 checkpoint 阶段的完整恢复示例。重启从 checkpoint 关联调用读取 input.messages 和 output，缺少 output 时先进行既定错误恢复，工具结果按进度复用。新 run 沿有效上下文追加新输入，不从展示事件回放恢复执行状态。摘要覆盖除 system prompt 外的全部有效历史，成功后以 user 消息提供摘要，本轮真实输入保留，后续沿摘要及未覆盖消息接续，不重加已覆盖原文。continuation 保持 assistant:a、user/tool:b、assistant:c，工具结果按原 ID 配对，取消跳过回填 content="aborted"；上述已确认行为不再列为待定。
- 配置与估算：统一配置字段的具体映射及有效范围。头尾比例已确定固定为 1:1，不作为配置项。裁剪阈值已确定默认 8192 个估算 token 且允许用户配置，具体配置键及有效范围继续细化。token 估算算法已统一为 ceil(UTF-8 字节数 / 4)，用于工具裁剪和整体请求预算检查。普通轮数、续写上限、模型和工具运行参数全部归现有 config 统一加载管理，不再为普通轮数及续写上限设计独立读取机制。工具定义保持代码内置；并发、超时及逻辑调用 max_retry 的既定执行生效规则保留，已用预算不清零，不新增 run 配置快照。
- 服务与生命周期：正式接口签名及文件装配、带版本和幂等参数的删除入口、只读业务回执查询、5 秒宿主退出预算内的停止实现。HTTP ready 前完成配置加载、数据库连接、恢复登记及占用重建，ready 后后台继续恢复，不等待所有旧 run 执行结束；该启动顺序已确定。

本文不授权数据库结构变更；表结构方案需讨论确定并获得明确请求后实施。
