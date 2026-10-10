# 界面数据接入

- `config_file_store.dart`：固定 data-dir 下的 config.yaml 用户覆盖值读取、运行字段更新与恢复；日志级别限定 debug/info/warn/error，路径校验非空及空字符。使用 yaml_edit 保留注释与未知字段，同一文件多实例读写串行化，临时文件刷新后在同一文件系统替换。

配置约束：应用内仅 Flutter 写用户配置，Go 只读取和监听。公开视图只返回日志级别、日志路径与数据库路径的用户覆盖值，未配置字段返回 null，默认值由 Go 原有 default.yaml 决定。恢复只移除三个运行字段，保留 app、desktop 和未知字段。保存结果 pathsChanged 只描述路径字段变化，数据库和日志路径在下次启动应用，不将保存配置当成资源迁移。启动层负责传入共同的绝对数据目录；此组件不新增 HTTP 接口或配置编辑页面。

- `http_notebook_repository.dart`：读取 Go 的今日、日期、月历和待确认快照，处理分页、提醒详情、确认、真实规则详情/列表/创建/更新/删除、时间转换与错误状态。
- `demo_notebook_repository.dart`：提供内存示例日程与提醒、预设规则投影、创建/编辑/删除和确认交互。

工程约束：HTTP 模式使用 Go 的快照、总数、游标和 occurrenceKey，RFC3339 转换为 UTC+8 展示；请求失败不回退示例数据。日程路径为 `/api/v1/scheduler/schedules`，提醒路径为 `/api/v1/notifications`；确认统一使用 `notificationIds` 数组。

创建接受 201，删除接受 204，更新使用 PUT 与 expectedRevision。每个编辑会话的结果不明重试保留相同 Idempotency-Key 和内容；服务 5xx/网络中断不自动改键，不同内容被阻止。操作成功刷新失败只提示读取失败，不伪装写入失败。

创建/更新提交前均校验单次时间的秒与亚秒为零，拒绝非分钟草稿，不先取整再发送。

日期计数读取服务端 `openScheduleCount`，与首分页长度和待确认总数分开缓存；分页加载不重复累加日程数，确认不减数。刷新失败保留原快照，未取得计数时保持未知。

周期写入通过 ScheduleRules 将独立时间项转换成现有 cron 数组，不改变 Go HTTP 契约；同频率不同日期/时间可无损编辑，混合频率保留原文。内存示例按独立项显示每次未来安排及短月月末去重，正式模式仍由 Go 投影。
