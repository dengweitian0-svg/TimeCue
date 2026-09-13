package app

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// ScheduleStore 定义应用服务使用的日程持久化能力。
type ScheduleStore interface {
	CreateSchedule(context.Context, domain.ScheduleCandidate) (domain.Schedule, error)
	GetSchedule(context.Context, domain.ScheduleID) (domain.Schedule, error)
	ListSchedules(context.Context, ListSchedulesRequest) (ListSchedulesResponse, error)
}

// ScheduleUpdater 定义日程更新所需的独立持久化能力。
// 与读取和创建能力分离，便于后续按日程类型扩展同一个更新入口。
type ScheduleUpdater interface {
	UpdateSchedule(context.Context, domain.ScheduleID, domain.ScheduleUpdateCandidate) (domain.Schedule, error)
}

// ScheduleDeleter 定义带乐观锁版本的日程删除能力。
type ScheduleDeleter interface {
	DeleteSchedule(context.Context, domain.ScheduleID, int64) (string, error)
}

// Clock 定义应用服务读取当前时间环境的能力。
type Clock interface {
	Snapshot(context.Context) (domain.TimeSnapshot, error)
}

// CreateScheduleRequest 表示创建单次或重复日程的输入，端点表示首次发生。
type CreateScheduleRequest struct {
	Title            string  `json:"title"`
	StartUnix        *int64  `json:"start_unix"`
	EndUnix          *int64  `json:"end_unix"`
	Cron             *string `json:"cron"`
	UntilUnix        *int64  `json:"until_unix"`
	CronPresent      bool    `json:"-"`
	UntilUnixPresent bool    `json:"-"`
}

// UpdateScheduleRequest 表示日程字段补丁和乐观锁版本。
// Present 字段用于保留 JSON 中显式携带 null 或 false 的存在性，避免把它们
// 与“未修改”混为一谈；当前单次更新分支会拒绝 cron、cancel 和 restore。
type UpdateScheduleRequest struct {
	Title            *string `json:"title"`
	StartUnix        *int64  `json:"start_unix"`
	EndUnix          *int64  `json:"end_unix"`
	Cron             *string `json:"cron"`
	UntilUnix        *int64  `json:"until_unix"`
	Cancel           *bool   `json:"cancel"`
	Restore          *bool   `json:"restore"`
	ExpectedRevision int64   `json:"expected_revision"`

	TitlePresent            bool `json:"-"`
	StartUnixPresent        bool `json:"-"`
	EndUnixPresent          bool `json:"-"`
	CronPresent             bool `json:"-"`
	UntilUnixPresent        bool `json:"-"`
	CancelPresent           bool `json:"-"`
	RestorePresent          bool `json:"-"`
	ExpectedRevisionPresent bool `json:"-"`
}

// DeleteScheduleRequest 表示带版本保护的永久删除请求。
type DeleteScheduleRequest struct {
	ExpectedRevision        int64 `json:"expected_revision"`
	ExpectedRevisionPresent bool  `json:"-"`
}

// ScheduleDTO 表示对外返回的日程视图。
type ScheduleDTO struct {
	ID              int64     `json:"id"`
	Title           string    `json:"title"`
	Cron            *string   `json:"cron"`
	UntilDateLocal  *string   `json:"until_date_local"`
	Open            bool      `json:"open"`
	FirstStartLocal string    `json:"first_start_local"`
	DurationMinutes int       `json:"duration_minutes"`
	Revision        int64     `json:"revision"`
	CreatedAtUTC    time.Time `json:"created_at_utc"`
	UpdatedAtUTC    time.Time `json:"updated_at_utc"`
}

// ListSchedulesRequest 表示列表分页输入。
type ListSchedulesRequest struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitzero"`
}

// ListSchedulesResponse 表示列表分页结果。
type ListSchedulesResponse struct {
	Items      []ScheduleDTO `json:"items"`
	NextCursor string        `json:"next_cursor,omitzero"`
}

// TimeBoundsDTO 表示可序列化的时间边界。
type TimeBoundsDTO struct {
	SingleMinUnix          int64 `json:"single_min_unix"`
	SingleMaxExclusiveUnix int64 `json:"single_max_exclusive_unix"`
	QueryMaxExclusiveUnix  int64 `json:"query_max_exclusive_unix"`
}
