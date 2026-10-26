# 数据库迁移

- `001_initial.up.sql`：原有日程与提醒核心业务表，notifications.status 支持 loaded。
- `002_schedule_changes.up.sql`：日程 revision、版本有效区间、停止投递、历史文本和写入回执。
- `003_ai_chat.up.sql`：会话、运行、模型调用、checkpoint、事件、工具调用六张 AI 表；user_version 为 3，原四张业务表保留。
- `embed.go`：嵌入 001/002/003 只读资源供启动 PrepareSchema 使用。
- `README.md`：初始化、备份升级及回退说明。

升级已有可识别 Flutter 库前使用 SQLite 一致性备份，事务内转换和验证，失败回滚；Wails 与未知结构拒绝。AI 关联统一使用 TEXT 业务 ID，不使用级联删除；JSON 校验、SQL NULL、五个唯一约束与状态 CHECK 按当前脚本固定，不增加查询索引。
