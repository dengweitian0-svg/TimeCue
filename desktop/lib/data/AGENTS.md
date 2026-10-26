# 界面数据接入

- `config_file_store.dart`：固定 data-dir 下的 config.yaml 用户覆盖值读取、运行字段更新与恢复；日志级别限定 debug/info/warn/error，路径校验非空及空字符。数据库目录保存时自动追加 timecue.db，读取提供所属目录；数据库及日志目录不能指向已有文件。使用 yaml_edit 保留注释与未知字段，同一文件多实例读写串行化，临时文件刷新后在同一文件系统替换。
- 旧 db.db_path 指向已存在的普通目录时，只读视图先补齐 timecue.db 再提取目录；normalizeLegacyDatabaseDirectory 在托管启动前仅修正该字段，保留其他配置和数据库内容。相对目录按固定 data-dir 判断，普通文件路径、缺省值和 SQLite 特殊数据源保持原样；修正与设置保存共用串行及原子替换。

配置约束：应用内仅 Flutter 写用户配置，Go 只读取和监听。公开视图只返回日志级别、日志路径与数据库路径的用户覆盖值，未配置字段返回 null，默认值由 Go 原有 default.yaml 决定。恢复只移除三个运行字段，保留 app、desktop 和未知字段。保存结果 pathsChanged 只描述路径字段变化，数据库和日志路径在下次启动应用，不将保存配置当成资源迁移。启动层负责传入共同的绝对数据目录；此组件不新增 HTTP 接口或配置编辑页面。

- `http_notebook_repository.dart`：读取 Go 的今日、日期、月历和待确认快照，处理分页、提醒详情、确认、真实规则详情/列表/创建/更新/删除、时间转换与错误状态。
- 同一 HttpNotebookRepository 实现 NotificationDeliveryGateway，复用原请求封装调用领取、开始、结果和执行者退出 POST 及根 /health；退出 POST 使用宿主传入的剩余 deadline。投递操作不改变本子 loading 和快照，不自报成员，回填校验 attempt/outcome 一致。健康 503 读取 data.error，业务错误继续读取顶层 error；请求失败不改写投递结果。
- 同一仓储实现 NotificationQueryGateway，读取持久提醒详情、task 历史及 attempt 冻结成员；只返回分页 DTO，不覆盖今日/历史缓存或 loading。任务请求不附 state/scope，详情核对返回 notificationId，复用原客户端和错误类型。组合刷新相容发布 recovery 摘要，失败保留原快照及错误标记。确认成功后先作废旧恢复未确认数，正式刷新再发布真实剩余数；不能按本次确认总数估减本轮计数，后续读取失败不继续展示已过期的提示数量。
- `demo_notebook_repository.dart`：提供内存示例日程与提醒、预设规则投影、创建/编辑/删除和确认交互。
- `http_chat_gateway.dart`：通过本机 HTTP/SSE 实现聊天与模型列表接口，映射严格聊天 DTO 和 `ChatHttpError`，支持启动门禁、运行失效及传输释放；SSE 游标使用 `Last-Event-ID`。

工程约束：HTTP 模式使用 Go 的快照、总数、游标和 occurrenceKey，RFC3339 转换为 UTC+8 展示；请求失败不回退示例数据。日程路径为 `/api/v1/scheduler/schedules`，提醒路径为 `/api/v1/notifications`；确认统一使用 `notificationIds` 数组。

创建接受 201，删除接受 204，更新使用 PUT 与 expectedRevision。每个编辑会话的结果不明重试保留相同 Idempotency-Key 和内容；服务 5xx/网络中断不自动改键，不同内容被阻止。操作成功刷新失败只提示读取失败，不伪装写入失败。

创建/更新提交前均校验单次时间的秒与亚秒为零，拒绝非分钟草稿，不先取整再发送。

日期计数读取服务端 `openScheduleCount`，与首分页长度和待确认总数分开缓存；分页加载不重复累加日程数，确认不减数。刷新失败保留原快照，未取得计数时保持未知。

周期写入通过 ScheduleRules 将独立时间项转换成现有 cron 数组，不改变 Go HTTP 契约；同频率不同日期/时间可无损编辑，混合频率保留原文。内存示例按独立项显示每次未来安排及短月月末去重，正式模式仍由 Go 投影。

HTTP 仓储使用 scope=today/history 的独立首分页、游标链、位置、加载/错误及代次，不把组合快照全历史页当作范围页。组合快照提供今日安排和房间完整总数；普通刷新从新首分页重建各范围当前位置，越界回到有效页。确认对已加载日期和两范围同步事实与总数，跨页 ID 归属只用于页面缓存更新；写入成功后的读取失败单独显示。

注入的北京时间墙上时钟检测跨日；新日期同时重读两范围首分页及今日安排，首分页响应日期不一致时只重试一次，不发布混合日期。旧游标 400 后废弃续页链，旧请求按全局及范围代次失效。跨日读取失败保留旧内容，pendingCurrent=false 禁止旧今日选择提交。演示仓储按计划时间及 ID 排序、分类已有事实，支持可控时钟；不触发平台投递。

- HttpNotebookRepository 的 managed 门禁初始只开放 health；握手后绑定预期身份，运行失效禁止新业务请求、作废旧读取并关闭连接。启动前失败保留 health 客户端供显式重试；不接管不同健康身份、不向新运行补交旧 attempt。
- 默认 HTTP 地址随 Flutter 构建模式选择端口：Debug/Profile 使用 1145，Release 使用 10086；scheduler 请求和根 `/health` 共用同一地址来源。
- 默认本地 HTTP 客户端对 loopback 地址直连，不经系统 HTTP 代理；调用方注入的客户端保留其代理行为。空正文、非法 JSON 和非对象响应提供含状态及端点的有限诊断，不暴露响应正文；204 仍作为无正文成功处理。

- 托管健康读取同时绑定 PID；运行身份/PID 错配立即封锁仓储并通过 onManagedRuntimeLost 回流到 ManagedBackend，进入全应用失效状态，不能仅停止通知桥后仍允许页面继续操作。

- 创建请求包含 provider，接受 202 新建/running 重放和 200 终态回执；GET /v1/model 只读取可用性，不传输 Key。
