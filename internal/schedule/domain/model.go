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
	Notes              string
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
	Notes              string
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
	Notes                 *string
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

// Occurrence 表示按需计算或由例外快照生成的一次具体发生。
type Occurrence struct {
	ScheduleID   ScheduleID
	OriginalDate string
	StartLocal   time.Time
	EndLocal     time.Time
	Title        string
	Notes        string
}

// OccurrenceOverride 表示某个原始日期对应的完整修改快照。
// SnapshotPresent 为 false 时保留未来取消语义所需的无快照例外。
type OccurrenceOverride struct {
	ScheduleID      ScheduleID
	OriginalDate    string
	StartLocal      time.Time
	DurationMinutes int
	Title           string
	Notes           string
	Open            bool
	SnapshotPresent bool
}

// OccurrenceOverrideCandidate 表示已经完成领域校验、等待原子写入的例外快照。
type OccurrenceOverrideCandidate struct {
	OriginalDate     string
	StartLocal       time.Time
	DurationMinutes  int
	Title            string
	Notes            string
	ExpectedRevision int64
	UpdatedAtUTC     time.Time
}

// OccurrenceUpdateResult 表示例外快照提交后的事实和父日程版本。
type OccurrenceUpdateResult struct {
	Override         OccurrenceOverride
	ScheduleRevision int64
}

// OccurrenceCancelCandidate 表示等待原子取消某次发生的操作候选。
type OccurrenceCancelCandidate struct {
	OriginalDate     string
	ExpectedRevision int64
	UpdatedAtUTC     time.Time
}

// OccurrenceRestoreCandidate 表示等待原子恢复某次发生的操作候选。
// RemoveCancellation 仅对没有快照的取消例外生效，表示恢复当前规则后移除该例外。
type OccurrenceRestoreCandidate struct {
	OriginalDate       string
	ExpectedRevision   int64
	UpdatedAtUTC       time.Time
	RemoveCancellation bool
}

// OccurrenceActionResult 表示取消或恢复提交后的例外状态和父日程版本。
// OverridePresent 为 false 时，表示恢复后已回到当前规则，没有持久化例外。
type OccurrenceActionResult struct {
	Override         OccurrenceOverride
	OverridePresent  bool
	ScheduleRevision int64
	Changed          bool
}
