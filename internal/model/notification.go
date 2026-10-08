package model

import (
	"errors"
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

// ErrInvalidAcknowledgement 表示确认集合含未来、已删除或非法提醒。
var ErrInvalidAcknowledgement = errors.New("invalid acknowledgement")

// NotificationPosition 是稳定分页所需的时间与主键边界。
type NotificationPosition struct {
	Time time.Time
	ID   int
}
