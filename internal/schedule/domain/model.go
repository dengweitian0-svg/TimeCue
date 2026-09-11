package domain

import "time"

// ScheduleID 表示日程的持久化标识。
type ScheduleID int64

// Schedule 表示已经持久化的日程定义。
type Schedule struct {
	ID                 ScheduleID
	Title              string
	Cron               *string
	Open               bool
	FirstStartLocal    time.Time
	OriginalStartLocal time.Time
	DurationMinutes    int
	Revision           int64
	CreatedAtUTC       time.Time
	UpdatedAtUTC       time.Time
}

// ScheduleCandidate 表示等待写入存储的日程候选。
type ScheduleCandidate struct {
	Title              string
	Cron               *string
	Open               bool
	FirstStartLocal    time.Time
	OriginalStartLocal time.Time
	DurationMinutes    int
	CreatedAtUTC       time.Time
	UpdatedAtUTC       time.Time
}
