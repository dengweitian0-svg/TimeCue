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
