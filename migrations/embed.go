package migrations

import _ "embed"

// Initial 保存新库的核心表定义。
//
//go:embed 001_initial.up.sql
var Initial string

// ScheduleChanges 保存规则历史和幂等回执的升级脚本。
//
//go:embed 002_schedule_changes.up.sql
var ScheduleChanges string

// AIChat 保存会话、运行、模型调用、checkpoint、事件及工具调用表。
//
//go:embed 003_ai_chat.up.sql
var AIChat string
