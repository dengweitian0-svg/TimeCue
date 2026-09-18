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

// ReminderAttemptStatus 表示一次提醒尝试的有限状态集合。
type ReminderAttemptStatus string

const (
	ReminderAttemptClaimed   ReminderAttemptStatus = "claimed"
	ReminderAttemptSubmitted ReminderAttemptStatus = "submitted"
	ReminderAttemptFailed    ReminderAttemptStatus = "failed"
	ReminderAttemptSkipped   ReminderAttemptStatus = "skipped"
)

// ReminderNotification 是提交给通知适配器的稳定提醒内容。
type ReminderNotification struct {
	Identity   domain.OccurrenceIdentity
	StartLocal time.Time
	EndLocal   time.Time
	Title      string
	Notes      string
}

// ReminderNotifier 定义同步提交一条提醒通知的窄端口。
type ReminderNotifier interface {
	Submit(context.Context, ReminderNotification) error
}

// ReminderAttemptStore 定义一次性提醒占用及结果回写的持久化端口。
type ReminderAttemptStore interface {
	ClaimReminderAttempt(context.Context, domain.OccurrenceIdentity, time.Time) (bool, error)
	SetReminderAttemptStatus(context.Context, domain.OccurrenceIdentity, ReminderAttemptStatus) error
}

// ReminderAttemptRecord 是提醒处理查询所需的持久化尝试视图。
type ReminderAttemptRecord struct {
	ID             int64
	Identity       domain.OccurrenceIdentity
	AttemptedAtUTC time.Time
	Status         ReminderAttemptStatus
	ProcessedAtUTC *time.Time
}

// RecoveryStatus 表示 scheduler 冷启动恢复子状态，不替换 RuntimeState。
type RecoveryStatus string

const (
	RecoveryStatusIdle    RecoveryStatus = "idle"
	RecoveryStatusRunning RecoveryStatus = "running"
	RecoveryStatusFailed  RecoveryStatus = "failed"
)

// RecoveryCandidateMode 区分新 occurrence Claim 与遗留 claimed 重占用。
type RecoveryCandidateMode string

const (
	RecoveryCandidateNew     RecoveryCandidateMode = "new"
	RecoveryCandidateReclaim RecoveryCandidateMode = "reclaim"
)

// RecoveryCheckpoint 是数据库 checkpoint 的受控视图。
type RecoveryCheckpoint struct {
	Exists         bool
	CheckpointUnix int64
	UpdatedAtUTC   time.Time
}

// RecoveryAttemptListRequest 表示恢复用提醒尝试的 ID keyset 分页请求。
type RecoveryAttemptListRequest struct {
	AfterID int64
	Limit   int
}

// RecoveryAttemptListResponse 是一批恢复需要判断的提醒尝试记录。
type RecoveryAttemptListResponse struct {
	Items      []ReminderAttemptRecord
	NextCursor int64
}

// RecoveryCandidate 保存已经由领域事实重建的恢复 occurrence。
type RecoveryCandidate struct {
	Occurrence domain.Occurrence
	Mode       RecoveryCandidateMode
	Revision   int64
}

// RecoveryBatch 是按发生时间排序且受数量上限约束的一批恢复候选。
type RecoveryBatch struct {
	Candidates        []RecoveryCandidate
	CheckpointUnix    int64
	RecoveryUntilUnix int64
}

// RecoveryClaimResult 表示批量 Claim 或 reclaim 后实际获得发送机会的身份。
type RecoveryClaimResult struct {
	Identity domain.OccurrenceIdentity
	Mode     RecoveryCandidateMode
}

// RecoveryBatchResult 是外部通知提交后需要写回的单条终态。
type RecoveryBatchResult struct {
	Identity domain.OccurrenceIdentity
	Status   ReminderAttemptStatus
}

// RecoveryBatchCommit 是事务 B 的结果和 checkpoint 提交视图。
type RecoveryBatchCommit struct {
	Results        []RecoveryBatchResult
	CheckpointUnix int64
	UpdatedAtUTC   time.Time
}

// RecoveryStore 定义冷启动恢复所需的 checkpoint、批量 Claim 和批量提交端口。
// 它与普通提醒端口分离，使恢复专属重占用动作不会扩大普通发送语义。
type RecoveryStore interface {
	GetSchedulerRecoveryCheckpoint(context.Context) (RecoveryCheckpoint, error)
	ListReminderAttemptsForRecovery(context.Context, RecoveryAttemptListRequest) (RecoveryAttemptListResponse, error)
	ClaimRecoveryBatch(context.Context, []domain.OccurrenceIdentity, time.Time) ([]RecoveryClaimResult, error)
	CommitRecoveryBatch(context.Context, RecoveryBatchCommit) error
}

// RecoveryEvent 是 scheduler 向 runtime/frontend 暴露的恢复状态事件。
// ErrorCode 只携带稳定分类，不暴露底层存储或通知错误细节。
type RecoveryEvent struct {
	Status    RecoveryStatus `json:"status"`
	ErrorCode string         `json:"error_code,omitzero"`
}

// RecoveryEventHandler 接收恢复开始、完成或失败事件。
type RecoveryEventHandler func(RecoveryEvent)

// ListPendingReminderAttemptsRequest 表示按数据库记录 ID 分页读取待处理尝试。
// 它是存储内部游标，不等同于对外按发生时间排序的不透明游标。
type ListPendingReminderAttemptsRequest struct {
	AfterID int64
	Limit   int
}

// ListPendingReminderAttemptsResponse 是一批满足 submitted 且未确认条件的尝试。
type ListPendingReminderAttemptsResponse struct {
	Items      []ReminderAttemptRecord
	NextCursor int64
}

// ReminderConfirmationResult 表示一次原子确认提交后的首次写入事实。
type ReminderConfirmationResult struct {
	ProcessedAtUTC time.Time
	Changed        bool
}

// ReminderProcessingStore 定义提醒处理状态查询和确认的窄持久化端口。
// 发送状态与处理时间通过不同操作维护，存储实现不得让发送回写覆盖处理状态。
type ReminderProcessingStore interface {
	ListPendingReminderAttempts(context.Context, ListPendingReminderAttemptsRequest) (ListPendingReminderAttemptsResponse, error)
	GetReminderAttempt(context.Context, domain.OccurrenceIdentity) (ReminderAttemptRecord, error)
	ConfirmReminderAttempt(context.Context, domain.OccurrenceIdentity, time.Time) (ReminderConfirmationResult, error)
}

// OccurrenceSourceReader 定义发送前一致读取单个日程及其例外快照的端口。
type OccurrenceSourceReader interface {
	GetOccurrenceSource(context.Context, domain.ScheduleID) (OccurrenceSourceDTO, error)
}

// Timer 表示调度器持有的唯一最近到期计时器。
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(time.Duration) bool
}

// TimerFactory 创建一个指定时长的计时器，便于测试控制时间推进。
type TimerFactory func(time.Duration) Timer

// ReminderLogger 是调度器所需的最小结构化日志能力。
type ReminderLogger interface {
	Debug(string, ...any)
	Info(string, ...any)
	Warn(string, ...any)
	Error(string, ...any)
}

// RuntimeState 表示提醒运行单元当前所处的生命周期状态。
type RuntimeState string

const (
	RuntimeStateRunning   RuntimeState = "running"
	RuntimeStateSuspended RuntimeState = "suspended"
	RuntimeStateStopping  RuntimeState = "stopping"
)

// RuntimeEventType 表示会使提醒候选失效或需要重建的运行时事件类型。
type RuntimeEventType string

const (
	RuntimeEventSuspend      RuntimeEventType = "suspend"
	RuntimeEventResume       RuntimeEventType = "resume"
	RuntimeEventClockChanged RuntimeEventType = "clock_changed"
	RuntimeEventStop         RuntimeEventType = "stop"
)

// RuntimeEvent 携带生命周期事件及当时观测到的环境事实。
type RuntimeEvent struct {
	Type     RuntimeEventType
	Snapshot domain.TimeSnapshot
}

// SchedulerStatus 是调度器运行状态的受控视图。
type SchedulerStatus struct {
	Available      bool           `json:"available"`
	LastError      string         `json:"last_error"`
	Epoch          uint64         `json:"epoch"`
	State          RuntimeState   `json:"state"`
	RecoveryStatus RecoveryStatus `json:"recovery_status"`
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
// 与“未修改”混为一谈；cron、截止日期和开放状态的显式字段也支持类型转换。
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
// overlaps 表示查询范围内该实际区间与另一个身份发生相交，跨分页结果也会被正确标记。
// 提交成功后的响应通过 conflict 返回基于已提交事实计算的最终冲突结果。
type OccurrenceDTO struct {
	ScheduleID       int64             `json:"schedule_id"`
	OriginalDate     string            `json:"original_date"`
	StartUnix        int64             `json:"start_unix"`
	EndUnix          int64             `json:"end_unix"`
	Title            string            `json:"title"`
	Notes            string            `json:"notes"`
	ScheduleRevision int64             `json:"schedule_revision"`
	Overlaps         bool              `json:"overlaps"`
	Conflict         *ConflictCheckDTO `json:"conflict,omitzero"`
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

// ReminderContentStatus 表示当前是否能够从日程事实重建发生正文。
type ReminderContentStatus string

const (
	ReminderContentAvailable   ReminderContentStatus = "available"
	ReminderContentUnavailable ReminderContentStatus = "unavailable"
)

// ReminderDetailStatus 表示提醒处理详情的受控状态。
type ReminderDetailStatus string

const (
	ReminderStatusPending        ReminderDetailStatus = "pending"
	ReminderStatusConfirmed      ReminderDetailStatus = "confirmed"
	ReminderStatusMissing        ReminderDetailStatus = "missing"
	ReminderStatusNotProcessable ReminderDetailStatus = "not_processable"
)

// ReminderDetailDTO 表示一次提醒的稳定身份、处理状态和当前可重建正文。
// 正文字段使用指针，内容不可用或已确认时不会伪造空正文。
type ReminderDetailDTO struct {
	ScheduleID     int64                 `json:"schedule_id"`
	OriginalDate   string                `json:"original_date"`
	AttemptedAtUTC time.Time             `json:"attempted_at_utc,omitzero"`
	StartUnix      *int64                `json:"start_unix,omitzero"`
	EndUnix        *int64                `json:"end_unix,omitzero"`
	Title          *string               `json:"title,omitzero"`
	Notes          *string               `json:"notes,omitzero"`
	ContentStatus  ReminderContentStatus `json:"content_status"`
	Status         ReminderDetailStatus  `json:"status"`
	ProcessedAtUTC *time.Time            `json:"processed_at_utc,omitzero"`
}

// PendingReminderDTO 是待处理列表中的提醒详情视图。
type PendingReminderDTO = ReminderDetailDTO

// ListPendingRemindersRequest 表示按当前发生开始时间倒序的待处理列表请求。
type ListPendingRemindersRequest struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitzero"`
}

// ListPendingRemindersResponse 表示待处理提醒分页结果。
type ListPendingRemindersResponse struct {
	Items      []PendingReminderDTO `json:"items"`
	NextCursor string               `json:"next_cursor,omitzero"`
}

// ReminderConfirmationDTO 表示确认事务提交后的幂等结果。
type ReminderConfirmationDTO struct {
	ScheduleID     int64     `json:"schedule_id"`
	OriginalDate   string    `json:"original_date"`
	ProcessedAtUTC time.Time `json:"processed_at_utc"`
	Changed        bool      `json:"changed"`
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
