# internal/schedule/app

- 本目录负责编排日程应用用例，并定义应用层与外部实现之间的窄契约。
- 当前文件：
  - `contracts.go`：`ScheduleStore`、独立的 `ScheduleUpdater`、`ScheduleDeleter`、`Clock` 端口，以及创建、更新、删除、列表请求和受控响应 DTO。
  - `service.go`：创建、更新、删除、读取、分页列表和时间边界查询用例。
  - `recurring.go`：重复整组编辑的定义合并和领域校验，保持未触及字段及原始发生身份。
  - `json.go`：`encoding/json/v2` 的创建、更新、删除请求解码，以及日程、列表、时间边界和错误响应编码边界。
  - `*_test.go`：验证创建、更新、删除和读取用例编排、契约替换、分页默认值和 JSON 行为。
- `Service.CreateSchedule` 必须先清理标题、检查必填字段和依赖，再只读取一次时间快照，完成领域校验后才调用 `ScheduleStore.CreateSchedule`。
- `Service.UpdateSchedule` 必须校验标识、期望版本和支持的字段，预读日程以补齐缺失端点并确认类型，再只读取一次时间快照；领域校验完成后才调用独立的 `ScheduleUpdater`。
- `Service.DeleteSchedule` 必须校验标识和期望版本，通过独立的 `ScheduleDeleter` 执行永久删除，并在失败时保留存储层已读取的标题。
- 列表用例使用稳定默认 `limit`，拒绝负数；分页游标的具体编码属于存储实现，应用层只传递请求和结果契约。
- DTO 是对外的受控视图；转换时不得暴露领域原始内部字段、数据库连接或底层错误细节。
- JSON 边界必须区分缺失字段与显式零值，严格拒绝非法数值和非对象的创建、更新、删除请求；错误响应只输出业务分类、消息、字段和允许的时间边界。
- 本目录不得直接调用 `time.Now`、SQL、文件系统或具体日志实现；新增外部能力应先扩展窄接口并保持生产实现可替换。
- 创建请求沿用 `start_unix/end_unix` 表示首次实际起止；省略 cron 创建单次，非空 cron 创建重复且必须提供 `until_unix`。该截止瞬间按一次快照的显式时区转换为包含式本地日期，不要求分钟精度。
- 更新请求支持重复定义的 cron、标题、首次起止和截止日期同类型编辑；省略字段保持原值，显式 null cron/截止日期返回 VALIDATION，单次携带重复字段仍拒绝。只更换 cron 时不修改其余业务字段，成功后 revision 加一并更新审计时间。
- 预读发现旧 revision 时返回 STALE_REVISION，不调用更新端口；存储事务仍承担最终并发守卫。非法规则或合并后的日期/端点校验失败均不得写入。
- `ScheduleDTO.until_date_local` 在单条读取、创建/更新响应和列表中均返回 `YYYY-MM-DD` 或 null；JSON 创建/更新边界都保留 cron 和 until_unix 的存在性。
