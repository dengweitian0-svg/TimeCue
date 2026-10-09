package model

import (
	"context"
	"time"
)

// RecoveryPosition 是本运行的继续位置，不写入数据库。
type RecoveryPosition struct {
	Facts      bool
	ScheduleID int
	Revision   int
	After      time.Time
	Fact       NotificationPosition
}

// RecoveryWindow 固定一次历史整理范围；每批最多处理 500 条事实。
type RecoveryWindow struct {
	Start, End, AsOf time.Time
	Position         RecoveryPosition
}

// PrepareRecoveryVersion 只计算下一批版本触发，不读取存储或平台。
type PrepareRecoveryVersion func(context.Context, ScheduleVersion, time.Time, time.Time, time.Time, int) ([]NotificationCandidate, time.Time, bool, error)

// RecoveryBatch 的位置只有在事务提交和内存交接成功后才能采用。
type RecoveryBatch struct {
	Notifications []*Notification
	Next          RecoveryPosition
	Done          bool
}
