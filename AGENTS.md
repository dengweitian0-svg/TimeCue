# TimeCue

- `scripts/build-release.ps1` 打包完整 Flutter Windows x64 目录、timecue-core.exe、update.py、发行说明、版本/文件校验清单和 ZIP；发行说明见 docs/RELEASE.md。正式发行目录的主实例窗口启动成功后会登记当前用户 `TIMECUE_INSTALL_DIR`，供 Python 更新脚本定位安装目录。主实例默认 managed，使用当前用户 LocalAppData/TimeCue；Go 在监听前显式初始化新库或备份升级可识别 Flutter 旧库，拒绝自动转换 Wails 数据。

Windows 桌面日程提醒应用：Go 提供日程、提醒和本地数据服务，Flutter 提供房间与日程本界面。已接通查询、确认、规则操作、分钟调度、全期间恢复与汇总、任务成员分页及投递领取/开始/回填协议；Go 支持固定数据目录、运行身份、托管 ready 和父端停止控制。Flutter 已实现 Windows 即时通知桥、运行内结果重传、通知点击只读定位与恢复进度/成员展示；已接入托盘、关闭隐藏、原生单实例唤回与激活参数转交；Flutter 已接入自有 Go 的隐藏创建、身份握手、共享退出和 Job 崩溃清理。

- [CONTEXT.md](docs/CONTEXT.md)：领域术语表，只定义业务概念，不记录实现方案。
- [cmd/flutter](cmd/flutter/AGENTS.md)：Go 本机 HTTP 服务启动、组件装配与退出清理。
- [internal/api](internal/api/AGENTS.md)：HTTP 路由、日程与提醒接口。
- [internal/app](internal/app/AGENTS.md)：服务装配、跨模块用例、正式操作边界与运行生命周期。
- [internal/agent](internal/agent/AGENTS.md)：真实聊天服务、模型适配器与持久化契约。
- [internal/middleware](internal/middleware/AGENTS.md)：请求日志、Host 校验、异常恢复与响应适配。
- [internal/service](internal/service/AGENTS.md)：schedule 管理日程规则与查询，notification 管理提醒、调度和投递，recovery 管理历史恢复与进度。
- [internal/model](internal/model/AGENTS.md)：日程、提醒、查询条件及共享业务类型。
- [internal/store](internal/store/AGENTS.md)：SQLite 连接、健康检查、日程与提醒持久化。
- [internal/config](internal/config/AGENTS.md)：只读加载配置、发布快照、目录监听及运行路径解析；用户配置由 Flutter 写入。
- [internal/log](internal/log/AGENTS.md)：全局日志、JSONL 输出与级别热更新。
- [desktop](desktop/AGENTS.md)：Flutter 桌面界面、HTTP 数据接入和内存演示。
- [.github/workflows](.github/workflows/AGENTS.md)：PR 快速检查、dev 集成构建和 main 发布验证。

# 约束
- 如果项目中存在 `.codex/` 目录，开始任务前先读取其中的 `AGENTS.md`（若存在），并遵循其适用规则。
- 代码注释使用中文；可导出方法或函数必须添加中文注释；模块变更同步对应 AGENTS.md 和工程说明。Go 管理日程规则与提醒状态，Flutter 通过业务接口操作；平台副作用结果回传 Go。开发分支与检查流程见 [贡献规范](docs/CONTRIBUTING.md)
- Go 结构体初始化传入两个及以上字段时，字段必须逐行书写，不得挤在同一行；完成后使用 gofmt 格式化。
- 任何改动数据库表结构的操作必须获取请求
- 在新增，删除，更新 HTTP 接口后，必须要先在 Apifox 创建测试并通过后才能继续前端的开发
- AGENTS.md 文档中，只需要声明当前模块的组成，功能，约束即可，不记录决策
- 当前项目不需要过多的工程约束，简单够用即可
- 临时文件、执行报告和验收记录只保留本地，不纳入提交，也不为它们新增 `.gitignore` 规则；`docs/archify/` 中随图表交付的执行回执与校验、验收证据作为文档产物一并提交。
- 需要根据功能创建新分支来开发，而不是在 dev 分支中开发
- Flutter 已接入显式 managed/external 模式、隐藏自有 Go、ready/health 身份门禁和共享退出回填；Job Object 在进程创建时关联，最终回收及宿主崩溃只清理自有进程。开发外连不停止手动 Go，启动失败可重试、运行中异常退出不自动重启
- CI 按 PR 路径选择 Go、Flutter 或 Windows Python 更新脚本检查，纯文档 dev PR 只运行轻量分类；dev 非文档 push 后执行完整 Windows 集成验证；所有 main PR 通过 Go Windows Checks 和 Flutter Windows Checks 执行完整发布构建与原生验证；手动运行发行工作流时上传发行产物。

- T01 文字对话使用真实 Chat Completions 与 SQLite 版本 3 的六张 AI 表；缺少 Key 仍正常启动，配置与当前切片边界见 [docs/CHAT.md](docs/CHAT.md)。
- T02 使用连续 seq 的五条/五秒事件批次、原子尾批和持久化 SSE 接续；Flutter 按需翻阅原始时间线、去重补缺并在 204 后恢复，最终正文独立使用 run.output，取消收尾保留占用，工具详情保留原始 JSON。不增加数据库结构或模型调用分组 DTO。
- T03 通过真实 SQLite 验证会话内原文幂等、创建互斥与全局三个活跃名额；重放先于新建门禁，配置拒绝、非法请求与创建失败不留新键。启动先重建占用，取消恢复不重发模型；HTTP 允许 cancelled 且 finished_at=null 的收尾概况，不以投影状态释放名额。
- T04 在同一逻辑 model_call 中持久化有限重试、退避期限与历史；默认首次外三次及 5/10/20 秒，仅无有效输出的明确暂时故障重试。Key 与超时在每次请求前热读取，请求内固定；部分输出失败保留内容及 usage，取消不重试。配置规范化冲突拒绝发布，明确的配置 503 不占前端运行名额。
- T05 新 run 沿最新已结束运行的完整 checkpoint 消息快照接续，保留正文、reasoning_content、工具配对及后端请求；中断说明独立置于旧轮全部消息之后。请求序列化字节统一用于预算、持久化及上游发送；预算失败原子创建 start/error/end，不创建调用或 checkpoint。普通轮数默认 20，通过现有调用记录排除 continuation/compact 统计，不新增数据库结构或 run 参数快照。
- T06 所有工具实现统一 Info/InvokableRun 接口并在 Registry 注册；三个只读日程工具通过正式单条/列表/日期入口查询。正常模型回复持久化接纳后逐项登记 running/原参数/tool_start/tool_use，再校验执行；结果、原始 JSON、配对消息、事件与 checkpoint 同事务保存，全批结果完成再回填模型。参数或业务错误只进入 tool_result，不直接失败运行；沿用版本 3，工具详情在 Flutter 保持原值并折叠展示。
- T07 创建/修改工具串行调用 App.WriteSchedule，修改必带 expectedRevision，保留未提交字段并复用正式规则校验与提醒联动；dangerous=false、工具键 SQL NULL、业务 key 为空，不按名称或时间去重。相对日期按每次实际解析时北京时间转换，明确时间和已提交结果直接复用；提示要求完整唯一直接执行、缺失或歧义先澄清、依赖操作分模型轮次、冲突不自动覆盖。HTTP 与数据库版本 3 不变，完整写入恢复仍属后续切片。
- T08 删除为唯一危险、串行工具，首次登记保存后台生成的 tool:{tool_call_id}:{uuid}，参数失败仍保留，重入复用原键。App.DeleteScheduleVersion 在同一正式业务事务优先核对回执，再检查版本、软删除、联动提醒及保存回执；App.ReadScheduleDeleteReceipt 独立只读，未命中不补做删除。业务与 AI 结果分属短事务，统一进度只负责原 ID 配对；数据库版本 3、HTTP 及 Flutter 不变，完整取消/写入恢复仍归后续切片。
- T09 工具并发默认 3、单次超时默认 30 秒，调度/尝试前热读且调低不取消在途；串行与其他有效尝试互斥，超时立即取消结算并隔离迟到返回。工具预算首次登记固定默认首次外 3 次，仅超时或明确暂时故障沿原记录/参数/适用键退避 5/10/20 秒；retry_wait 不回填最终结果，实际重试增加计数并原子保存 running/tool_use，全批最终结果完成才继续模型。沿用数据库版本 3，完整写入恢复仍归后续切片。

- T10 取消先持久化 wait_for_cancel 并返回 cancelled 投影，收尾期间保留名额与 SSE；在途工具等待实际结果或原期限，未开始及退避项沿统一进度保存 skipped/aborted，真实结果不被取消覆盖。取消恢复只核对既有工具结果或删除回执，无法核实记结果未确认，不补做业务。会话同步软删除立即隐藏所有新查询，子记录与日程保留；Flutter 直接删除活动会话，原 SSE 跟踪收尾，断线后准入以服务端实际占用为准。

- T11 正常 length 在同一 run 内新建 continuation 调用，统一 max_continue 默认 5 次，与摘要续写共用已有记录计数，不占普通轮数或原网络重试预算。正文/思考保留独立消息边界和增量事件；截断工具按原 ID 回填解析错误，格式补全仅用于模型反馈、不执行、不改原参数，同批完整工具结果配齐后才续写。run.output 只拼接最后普通轮及其续写，重启沿实际请求边界恢复；数据库版本 3、HTTP 和 Flutter 不变。
