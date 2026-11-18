package model

import "time"

// RecoveryView 区分历史整理与真实投递，未知总数固定为 null。
type RecoveryView struct {
	RecoveryID          string    `json:"recoveryId"`
	State               string    `json:"state"`
	Through             time.Time `json:"through"`
	MaterializedCount   int       `json:"materializedCount"`
	MissedTotal         *int      `json:"missedTotal"`
	DisplayableCount    int       `json:"displayableCount"`
	UnacknowledgedCount int       `json:"unacknowledgedCount"`
	DeliveryTaskID      string    `json:"deliveryTaskId"`
}
