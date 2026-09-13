package domain

import "time"

// MonthEndPolicyClamp 表示月规则在短月中回退到当月最后一天。
const MonthEndPolicyClamp = "clamp"

// ScheduleID 表示日程的持久化标识。
type ScheduleID int64

// Schedule 表示已经持久化的日程定义。
type Schedule struct {
	ID                 ScheduleID
	Title              string
	Cron               *string
	UntilDateLocal     *string
	MonthEndPolicy     *string
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
	UntilDateLocal     *string
	MonthEndPolicy     *string
	Open               bool
	FirstStartLocal    time.Time
	OriginalStartLocal time.Time
	DurationMinutes    int
	CreatedAtUTC       time.Time
	UpdatedAtUTC       time.Time
}

// ScheduleUpdateCandidate 表示经过应用层校验、等待条件写入的日程补丁。
// 指针字段表示该字段需要写入；CronPresent 单独表示请求显式携带了 cron，
// 即使其值为 nil 也不能把它误当成“保持不变”；MonthEndPolicyPresent
// 同理用于区分保留策略和显式清空策略。
type ScheduleUpdateCandidate struct {
	Title                 *string
	FirstStartLocal       *time.Time
	DurationMinutes       *int
	Cron                  *string
	CronPresent           bool
	UntilDateLocal        *string
	MonthEndPolicy        *string
	MonthEndPolicyPresent bool
	UpdatedAtUTC          time.Time
	ExpectedRevision      int64
}

// UpdateScheduleCandidate 是更新候选的兼容别名，便于调用方按操作命名使用契约。
type UpdateScheduleCandidate = ScheduleUpdateCandidate

// IsRecurring 判断日程是否已经携带周期规则。
func (s Schedule) IsRecurring() bool {
	return s.Cron != nil
}
