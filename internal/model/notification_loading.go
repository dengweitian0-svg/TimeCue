package model

import "time"

// ScheduleVersion 保存触发归属的版本边界；触发严格晚于 From，不晚于 Until。
type ScheduleVersion struct {
	Schedule Schedule
	From     time.Time
	Until    *time.Time
}

// NotificationWindow 是一次预加载或完整分钟核验的事务范围。
type NotificationWindow struct {
	Start, End, AsOf time.Time
	ScheduleID       int
	FreezeDue        bool
}

// NotificationCandidate 是纯规则计算的输出，不包含执行结果。
type NotificationCandidate struct {
	ScheduleID  int
	NotifyTime  time.Time
	Name        string
	Description *string
}

// PrepareNotificationWindow 只使用事务传入的版本计算候选，不访问外部资源。
type PrepareNotificationWindow func([]ScheduleVersion, time.Time, time.Time) ([]NotificationCandidate, error)

// NotificationBatch 同时保留完整事实与当前可加载成员，避免分页拆组。
type NotificationBatch struct {
	Notifications []*Notification
	EligibleIDs   []int
}
