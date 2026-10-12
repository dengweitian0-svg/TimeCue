package model

import "time"

// UnacknowledgedRange 限定到期未确认事实；下界含端点，上界不含端点。
// 零值边界表示不增加该方向的日期条件，AsOf 始终包含端点。
type UnacknowledgedRange struct {
	AsOf  time.Time
	Start time.Time
	End   time.Time
}
