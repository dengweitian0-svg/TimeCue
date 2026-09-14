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

// OccurrenceReader 定义应用层读取某日程例外的窄契约。
type OccurrenceReader interface {
	GetOccurrenceOverride(context.Context, domain.ScheduleID, string) (domain.OccurrenceOverride, error)
	ListOccurrenceOverrides(context.Context, domain.ScheduleID) ([]domain.OccurrenceOverride, error)
}

// OccurrenceUpdater 定义以父日程版本保护例外写入的窄契约。
type OccurrenceUpdater interface {
	UpdateOccurrence(context.Context, domain.ScheduleID, domain.OccurrenceOverrideCandidate) (domain.OccurrenceUpdateResult, error)
}

// OccurrenceCanceller 定义取消某次发生所需的原子持久化能力。
type OccurrenceCanceller interface {
	CancelOccurrence(context.Context, domain.ScheduleID, domain.OccurrenceCancelCandidate) (domain.OccurrenceActionResult, error)
}

// OccurrenceRestorer 定义恢复某次发生所需的原子持久化能力。
type OccurrenceRestorer interface {
	RestoreOccurrence(context.Context, domain.ScheduleID, domain.OccurrenceRestoreCandidate) (domain.OccurrenceActionResult, error)
}

// ScheduleCanceller 定义取消整组日程所需的原子持久化能力。
type ScheduleCanceller interface {
	CancelSchedule(context.Context, domain.ScheduleID, domain.ScheduleActionCandidate) (domain.ScheduleActionResult, error)
}

// ScheduleRestorer 定义恢复整组日程所需的原子持久化能力。
type ScheduleRestorer interface {
	RestoreSchedule(context.Context, domain.ScheduleID, domain.ScheduleActionCandidate) (domain.ScheduleActionResult, error)
}

// Clock 定义应用服务读取当前时间环境的能力。
type Clock interface {
	Snapshot(context.Context) (domain.TimeSnapshot, error)
}

// CreateScheduleRequest 表示创建单次或重复日程的输入，端点表示首次发生。
type CreateScheduleRequest struct {
	Title            string  `json:"title"`
	Notes            string  `json:"notes"`
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
	Notes            *string `json:"notes"`
	StartUnix        *int64  `json:"start_unix"`
	EndUnix          *int64  `json:"end_unix"`
	Cron             *string `json:"cron"`
	UntilUnix        *int64  `json:"until_unix"`
	Cancel           *bool   `json:"cancel"`
	Restore          *bool   `json:"restore"`
	ExpectedRevision int64   `json:"expected_revision"`

	TitlePresent            bool `json:"-"`
	NotesPresent            bool `json:"-"`
	StartUnixPresent        bool `json:"-"`
	EndUnixPresent          bool `json:"-"`
	CronPresent             bool `json:"-"`
	UntilUnixPresent        bool `json:"-"`
	CancelPresent           bool `json:"-"`
	RestorePresent          bool `json:"-"`
	ExpectedRevisionPresent bool `json:"-"`
}

// UpdateOccurrenceRequest 表示某个原始日期的例外补丁和父日程版本。
// 缺失字段从当前有效发生继承，写入端口始终接收完整快照。
type UpdateOccurrenceRequest struct {
	OriginalDate     string  `json:"original_date"`
	StartUnix        *int64  `json:"start_unix"`
	EndUnix          *int64  `json:"end_unix"`
	Title            *string `json:"title"`
	Notes            *string `json:"notes"`
	ExpectedRevision int64   `json:"expected_revision"`

	OriginalDatePresent     bool `json:"-"`
	StartUnixPresent        bool `json:"-"`
	EndUnixPresent          bool `json:"-"`
	TitlePresent            bool `json:"-"`
	NotesPresent            bool `json:"-"`
	ExpectedRevisionPresent bool `json:"-"`
}

// ScheduleActionRequest 表示取消或恢复整组日程的乐观锁请求。
type ScheduleActionRequest struct {
	ExpectedRevision        int64 `json:"expected_revision"`
	ExpectedRevisionPresent bool  `json:"-"`
}

// OccurrenceActionRequest 表示取消或恢复某个原始日期的操作请求。
type OccurrenceActionRequest struct {
	OriginalDate     string `json:"original_date"`
	ExpectedRevision int64  `json:"expected_revision"`

	OriginalDatePresent     bool `json:"-"`
	ExpectedRevisionPresent bool `json:"-"`
}

// DeleteScheduleRequest 表示带版本保护的永久删除请求。
type DeleteScheduleRequest struct {
	ExpectedRevision        int64 `json:"expected_revision"`
	ExpectedRevisionPresent bool  `json:"-"`
}

// PreviewChangeRequest 表示一次只读变更预览，操作类型决定需要携带的字段。
// 编辑载荷复用正式写入请求的字段，保证预览和提交使用同一套业务校验。
type PreviewChangeRequest struct {
	Operation        string                   `json:"operation"`
	ScheduleID       int64                    `json:"schedule_id"`
	OriginalDate     string                   `json:"original_date"`
	ExpectedRevision int64                    `json:"expected_revision"`
	Create           *CreateScheduleRequest   `json:"create,omitzero"`
	ScheduleUpdate   *UpdateScheduleRequest   `json:"schedule_update,omitzero"`
	OccurrenceUpdate *UpdateOccurrenceRequest `json:"occurrence_update,omitzero"`
	OccurrenceAction *OccurrenceActionRequest `json:"occurrence_action,omitzero"`
	ScheduleAction   *ScheduleActionRequest   `json:"schedule_action,omitzero"`

	OperationPresent        bool `json:"-"`
	ScheduleIDPresent       bool `json:"-"`
	OriginalDatePresent     bool `json:"-"`
	ExpectedRevisionPresent bool `json:"-"`
}

// ChangePreviewDTO 表示变更前预览出的下一次冲突范围，它是只读投影而不是已提交事实。
type ChangePreviewDTO struct {
	Operation string           `json:"operation"`
	Conflict  ConflictCheckDTO `json:"conflict"`
	Checked   bool             `json:"checked"`
	HasMore   bool             `json:"has_more"`
}

// ConflictCheckDTO 表示一次冲突检查的受控视图，包含明确状态和实际命中。
// 未检查时 reason 说明原因，调用方不能把它当作“整个系列永远无冲突”。
type ConflictCheckDTO struct {
	Status           string                 `json:"status"`
	Checked          bool                   `json:"checked"`
	Reason           string                 `json:"reason,omitzero"`
	Target           *OccurrenceIdentityDTO `json:"target,omitzero"`
	BatchUnix        int64                  `json:"batch_unix,omitzero"`
	OccurrenceCount  int                    `json:"occurrence_count"`
	SameScheduleOnly bool                   `json:"same_schedule_only"`
	Conflicts        []ConflictDTO          `json:"conflicts"`
}

// ConflictDTO 表示目标与其他身份发生之间的一次重叠。
type ConflictDTO struct {
	Target      OccurrenceIdentityDTO `json:"target"`
	Other       OccurrenceIdentityDTO `json:"other"`
	SameGroup   bool                  `json:"same_group"`
	OverlapUnix int64                 `json:"overlap_unix"`
}

// OccurrenceIdentityDTO 表示不暴露内部对象的稳定发生身份和实际时间。
type OccurrenceIdentityDTO struct {
	ScheduleID   int64  `json:"schedule_id"`
	OriginalDate string `json:"original_date"`
	Title        string `json:"title"`
	StartUnix    int64  `json:"start_unix"`
	EndUnix      int64  `json:"end_unix"`
}

// ScheduleDTO 表示对外返回的日程视图。
// 提交成功后的响应通过 conflict 返回基于已提交事实计算的最终冲突结果。
type ScheduleDTO struct {
	ID                     int64                    `json:"id"`
	Title                  string                   `json:"title"`
	Notes                  string                   `json:"notes"`
	Cron                   *string                  `json:"cron"`
	UntilDateLocal         *string                  `json:"until_date_local"`
	MonthEndPolicy         *string                  `json:"month_end_policy"`
	Open                   bool                     `json:"open"`
	FirstStartLocal        string                   `json:"first_start_local"`
	DurationMinutes        int                      `json:"duration_minutes"`
	Revision               int64                    `json:"revision"`
	PreservedOverrideCount int                      `json:"preserved_override_count"`
	CancelledOccurrences   []CancelledOccurrenceDTO `json:"cancelled_occurrences,omitzero"`
	Conflict               *ConflictCheckDTO        `json:"conflict,omitzero"`
	CreatedAtUTC           time.Time                `json:"created_at_utc"`
	UpdatedAtUTC           time.Time                `json:"updated_at_utc"`
}

// CancelledOccurrenceDTO 表示可供调用方查找和恢复的持久化取消例外。
// 空快照取消只返回稳定身份；完整快照取消额外返回保存的发生内容。
type CancelledOccurrenceDTO struct {
	OriginalDate    string  `json:"original_date"`
	SnapshotPresent bool    `json:"snapshot_present"`
	StartUnix       *int64  `json:"start_unix,omitzero"`
	EndUnix         *int64  `json:"end_unix,omitzero"`
	Title           *string `json:"title,omitzero"`
	Notes           *string `json:"notes,omitzero"`
}

// OccurrenceDTO 表示一次发生的实际时间和完整文本快照。
type OccurrenceDTO struct {
	ScheduleID       int64  `json:"schedule_id"`
	OriginalDate     string `json:"original_date"`
	StartUnix        int64  `json:"start_unix"`
	EndUnix          int64  `json:"end_unix"`
	Title            string `json:"title"`
	Notes            string `json:"notes"`
	ScheduleRevision int64  `json:"schedule_revision"`
}

// OccurrenceActionDTO 表示取消或恢复提交后的受控发生状态。
// 快照字段为空时，表示该状态没有持久化发生快照。
type OccurrenceActionDTO struct {
	ScheduleID       int64             `json:"schedule_id"`
	OriginalDate     string            `json:"original_date"`
	Cancelled        bool              `json:"cancelled"`
	ScheduleOpen     bool              `json:"schedule_open"`
	SnapshotPresent  bool              `json:"snapshot_present"`
	StartUnix        *int64            `json:"start_unix,omitzero"`
	EndUnix          *int64            `json:"end_unix,omitzero"`
	Title            *string           `json:"title,omitzero"`
	Notes            *string           `json:"notes,omitzero"`
	ScheduleRevision int64             `json:"schedule_revision"`
	Conflict         *ConflictCheckDTO `json:"conflict,omitzero"`
}

// ScheduleActionDTO 表示取消或恢复整组日程的受控结果。
type ScheduleActionDTO struct {
	ScheduleID   int64             `json:"schedule_id"`
	Open         bool              `json:"open"`
	Revision     int64             `json:"revision"`
	Conflict     *ConflictCheckDTO `json:"conflict,omitzero"`
	UpdatedAtUTC time.Time         `json:"updated_at_utc"`
}

// 变更操作类型常量限制预览入口只接受本切片支持的只读操作。
const (
	ChangeOperationCreate            = "create"
	ChangeOperationUpdateSchedule    = "update_schedule"
	ChangeOperationRestoreSchedule   = "restore_schedule"
	ChangeOperationUpdateOccurrence  = "update_occurrence"
	ChangeOperationRestoreOccurrence = "restore_occurrence"
)

// OccurrenceQueryStore 定义查询发生所需的 generation 和分批源读取能力。
type OccurrenceQueryStore interface {
	QueryGeneration(context.Context) (uint64, error)
	ListOccurrenceSources(context.Context, ListOccurrenceSourcesRequest) (ListOccurrenceSourcesResponse, error)
}

// ListOccurrenceSourcesRequest 表示按日程 ID 游标读取查询源的内部请求。
type ListOccurrenceSourcesRequest struct {
	AfterScheduleID domain.ScheduleID
	Limit           int
}

// ListOccurrenceSourcesResponse 表示一批日程定义及其例外快照。
type ListOccurrenceSourcesResponse struct {
	Items      []OccurrenceSourceDTO
	NextCursor domain.ScheduleID
}

// OccurrenceSourceDTO 是应用层查询源的受控快照，不包含数据库连接或实例状态。
type OccurrenceSourceDTO struct {
	Schedule  domain.Schedule
	Overrides []domain.OccurrenceOverride
}

// OccurrenceSource 是查询源 DTO 的兼容别名。
type OccurrenceSource = OccurrenceSourceDTO

// ListOccurrencesRequest 表示整数秒半开时间区间和发生游标。
type ListOccurrencesRequest struct {
	FromUnix int64  `json:"from_unix"`
	ToUnix   int64  `json:"to_unix"`
	Limit    int    `json:"limit"`
	Cursor   string `json:"cursor,omitzero"`

	FromUnixPresent bool `json:"-"`
	ToUnixPresent   bool `json:"-"`
	LimitPresent    bool `json:"-"`
}

// ListOccurrencesResponse 表示稳定排序的发生分页结果。
type ListOccurrencesResponse struct {
	Items      []OccurrenceDTO `json:"items"`
	NextCursor string          `json:"next_cursor,omitzero"`
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
