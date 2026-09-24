package model

import (
	"time"
)

// Notification 是一次持久化的日程触发记录。
// 投递结果与用户确认分别保存。
type Notification struct {
	ID             int
	ScheduleID     int
	Status         NotifyStatus
	NotifyTime     time.Time
	AcknowledgedAt *time.Time
	Deleted        bool
}

type NotifyStatus string

const (
	NotifyPending NotifyStatus = "pending"
	NotifySent    NotifyStatus = "sent"
	NotifyFailed  NotifyStatus = "failed"
)
