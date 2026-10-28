package model

import (
	"errors"
	"time"
)

var ErrScheduleConflict = errors.New("schedule revision conflict")
var ErrIdempotencyConflict = errors.New("idempotency key reused with different request")

// ScheduleChange 是正式创建或更新的输入；ID 为零表示创建。
type ScheduleChange struct {
	Schedule         Schedule
	ExpectedRevision int
	Key              string
	Digest           string
}

// ScheduleDelete 是带版本及后端执行身份的正式删除请求，不携带可覆盖的规则。
type ScheduleDelete struct {
	ID               int
	ExpectedRevision int
	Key              string
	Digest           string
}

// ScheduleWrite 保存操作的原始结果，重复请求返回同一结果及生效时刻。
type ScheduleWrite struct {
	Schedule     *Schedule
	Notification *Notification
	EffectiveAt  time.Time
	// Before 和 Replayed 是本次调用的事实，不进入持久化幂等回执。
	Before   *Schedule `json:"-"`
	Replayed bool      `json:"-"`
}

// PrepareScheduleChange 在存储事务内校验当前规则并计算首个未来时间。
// 实现由应用层提供，存储不解释 cron。
type PrepareScheduleChange func(current *Schedule, effectiveAt time.Time) (*time.Time, error)
