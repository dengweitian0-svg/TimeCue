package model

import "time"

// OccurrenceView 是事实提醒与未来规则投影共用的只读展示契约。
type OccurrenceView struct {
	OccurrenceKey  string        `json:"occurrenceKey"`
	ScheduleID     int           `json:"scheduleId"`
	NotificationID *int          `json:"notificationId"`
	Name           string        `json:"name"`
	Description    *string       `json:"description"`
	NotifyTime     time.Time     `json:"notifyTime"`
	Source         string        `json:"source"`
	DeliveryStatus *NotifyStatus `json:"deliveryStatus"`
	AcknowledgedAt *time.Time    `json:"acknowledgedAt"`
	Editable       bool          `json:"editable"`
}

// OccurrencePage 包含服务端统计和不透明续页游标。
type OccurrencePage struct {
	Items             []OccurrenceView `json:"items"`
	Total             int              `json:"total"`
	NextCursor        string           `json:"nextCursor"`
	AsOf              time.Time        `json:"asOf"`
	OpenScheduleCount *int             `json:"openScheduleCount,omitempty"`
	Recovery          *RecoveryView    `json:"recovery,omitempty"`
}

// ScheduleQueryResult 是日程查询的只读组合结果。
type ScheduleQueryResult struct {
	Date           string         `json:"date"`
	AsOf           time.Time      `json:"asOf"`
	Occurrences    OccurrencePage `json:"occurrences"`
	Unacknowledged OccurrencePage `json:"unacknowledged"`
	Recovery       *RecoveryView  `json:"recovery"`
}
