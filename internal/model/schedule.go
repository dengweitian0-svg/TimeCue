package model

import (
	"time"
)

type ScheduleType string

const (
	SingleSchedule ScheduleType = "single" // 单次日程
	PeriodSchedule ScheduleType = "period" // 周期性日程
)

// Schedule 现阶段支持两种日程, 同一个 Schedule 只能有其中一个类型
// single: 只触发一次, 触发完就失效
// Period: 重复日程, 通过 cron 计算触发日期, 支持定义多个日期
type Schedule struct {
	ID          int
	Name        string
	Type        ScheduleType
	Description *string
	Period      *Period
	Single      *Single
	Create_at   time.Time
	Update_at   time.Time
}

// Period 周期日程
type Period struct {
	Open bool     // 当前周期日程是否开放
	Cron []string // 支持多个日期
}

// Single 单次日程
type Single struct {
	NotifyTime time.Time
}

// Query 是进行范围查询时的 WHERE 过滤条件
type QueryOption struct {
	Name      *string       // 不传默认不按 name 查找
	Offset    *int          // 不传默认从 0 开始
	Limit     *int          // limit 上限为 LimitMax, 不传默认为 50
	AfterTime *time.Time    // 提供查找触发时间在传入时间之后的日程, 不传默认调用时的时间
	Type      *ScheduleType // 不指定类型, 则默认查找所有类型的
}
