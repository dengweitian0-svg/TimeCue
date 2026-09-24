package model

import (
	"time"
)

// Notiification 只管什么时候提醒哪个日程, 其他的都不管
type Notification struct {
	ID         int
	ScheduleID int
	Status     NotifyStatus
	NotifyTime time.Time
}

type NotifyStatus string

const (
	pending   NotifyStatus = "pending"
	sended    NotifyStatus = "sended"
	failed    NotifyStatus = "failed"
	cancelled NotifyStatus = "cancelled"
)
