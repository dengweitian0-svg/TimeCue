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

// Clock 定义应用服务读取当前时间环境的能力。
type Clock interface {
	Snapshot(context.Context) (domain.TimeSnapshot, error)
}

// CreateScheduleRequest 表示创建单次日程的输入。
type CreateScheduleRequest struct {
	Title     string `json:"title"`
	StartUnix *int64 `json:"start_unix"`
	EndUnix   *int64 `json:"end_unix"`
}

// ScheduleDTO 表示对外返回的日程视图。
type ScheduleDTO struct {
	ID              int64     `json:"id"`
	Title           string    `json:"title"`
	Cron            *string   `json:"cron"`
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
