package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const defaultScheduleListLimit = 50

// Service 编排日程应用用例。
type Service struct {
	store ScheduleStore
	clock Clock
}

// NewService 创建日程应用服务。
func NewService(store ScheduleStore, clock Clock) *Service {
	return &Service{store: store, clock: clock}
}

// CreateSchedule 创建并持久化单次日程。
func (s *Service) CreateSchedule(ctx context.Context, request CreateScheduleRequest) (ScheduleDTO, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return ScheduleDTO{}, domain.NewValidation("title must not be empty", "title")
	}
	if request.StartUnix == nil {
		return ScheduleDTO{}, domain.NewValidation("start time is required", "start_unix")
	}
	if request.EndUnix == nil {
		return ScheduleDTO{}, domain.NewValidation("end time is required", "end_unix")
	}
	if s.clock == nil {
		return ScheduleDTO{}, errors.New("schedule clock is not configured")
	}
	if s.store == nil {
		return ScheduleDTO{}, errors.New("schedule store is not configured")
	}

	// 每个请求只读取一次时间环境，确保边界计算和审计时间来自同一个快照。
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	candidate, err := domain.ValidateSingleEndpoints(snapshot, *request.StartUnix, *request.EndUnix)
	if err != nil {
		return ScheduleDTO{}, err
	}
	candidate.Title = title

	// 领域校验是进入存储副作用前的最后守卫，非法请求不能调用写方法。
	schedule, err := s.store.CreateSchedule(ctx, candidate)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("create schedule: %w", err)
	}
	return scheduleDTO(schedule), nil
}

// UpdateSchedule 对日程应用字段补丁并使用版本号保护并发更新。
func (s *Service) UpdateSchedule(ctx context.Context, id domain.ScheduleID, request UpdateScheduleRequest) (ScheduleDTO, error) {
	if id <= 0 {
		return ScheduleDTO{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	if request.ExpectedRevision <= 0 {
		return ScheduleDTO{}, domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	if request.CronPresent || request.Cron != nil {
		// 当前入口只实现单次日程；显式 cron 是类型转换请求，不能静默丢弃。
		return ScheduleDTO{}, domain.NewValidation("cron cannot be changed on a single schedule", "cron")
	}
	if request.CancelPresent || request.Cancel != nil {
		return ScheduleDTO{}, domain.NewValidation("cancel is not applicable to schedule update", "cancel")
	}
	if request.RestorePresent || request.Restore != nil {
		return ScheduleDTO{}, domain.NewValidation("restore is not applicable to schedule update", "restore")
	}

	titlePresent := request.TitlePresent || request.Title != nil
	startPresent := request.StartUnixPresent || request.StartUnix != nil
	endPresent := request.EndUnixPresent || request.EndUnix != nil
	if !titlePresent && !startPresent && !endPresent {
		return ScheduleDTO{}, domain.NewValidation("at least one schedule field must be provided", "title", "start_unix", "end_unix")
	}
	if titlePresent {
		if request.Title == nil {
			return ScheduleDTO{}, domain.NewValidation("title must be a string", "title")
		}
		if strings.TrimSpace(*request.Title) == "" {
			return ScheduleDTO{}, domain.NewValidation("title must not be empty", "title")
		}
	}
	if startPresent && request.StartUnix == nil {
		return ScheduleDTO{}, domain.NewValidation("start time must be an integer", "start_unix")
	}
	if endPresent && request.EndUnix == nil {
		return ScheduleDTO{}, domain.NewValidation("end time must be an integer", "end_unix")
	}
	if s.store == nil {
		return ScheduleDTO{}, errors.New("schedule store is not configured")
	}
	updater, ok := s.store.(ScheduleUpdater)
	if !ok {
		return ScheduleDTO{}, errors.New("schedule store does not support updates")
	}
	if s.clock == nil {
		return ScheduleDTO{}, errors.New("schedule clock is not configured")
	}

	// 预读只用于补齐缺失端点和识别当前日程类型；存储事务会再次读取并检查版本。
	existing, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("read schedule %d for update: %w", id, err)
	}
	if existing.IsRecurring() {
		return ScheduleDTO{}, domain.NewValidation("recurring schedule update is not supported", "cron")
	}

	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	candidate := domain.ScheduleUpdateCandidate{
		ExpectedRevision: request.ExpectedRevision,
		UpdatedAtUTC:     snapshot.Now.UTC(),
	}
	if titlePresent {
		title := strings.TrimSpace(*request.Title)
		candidate.Title = &title
	}
	if startPresent || endPresent {
		startUnix, endUnix, err := deriveUpdateEndpoints(existing, request.StartUnix, request.EndUnix)
		if err != nil {
			return ScheduleDTO{}, err
		}
		validated, err := domain.ValidateSingleEndpoints(snapshot, startUnix, endUnix)
		if err != nil {
			return ScheduleDTO{}, err
		}
		candidate.FirstStartLocal = &validated.FirstStartLocal
		candidate.DurationMinutes = &validated.DurationMinutes
	}

	updated, err := updater.UpdateSchedule(ctx, id, candidate)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("update schedule %d: %w", id, err)
	}
	return scheduleDTO(updated), nil
}

// DeleteSchedule 永久删除日程并保留存储层返回的标题和失败上下文。
func (s *Service) DeleteSchedule(ctx context.Context, id domain.ScheduleID, request DeleteScheduleRequest) (string, error) {
	if id <= 0 {
		return "", domain.NewValidation("schedule ID must be positive", "id")
	}
	if request.ExpectedRevision <= 0 {
		return "", domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	if s.store == nil {
		return "", errors.New("schedule store is not configured")
	}
	deleter, ok := s.store.(ScheduleDeleter)
	if !ok {
		return "", errors.New("schedule store does not support deletion")
	}
	title, err := deleter.DeleteSchedule(ctx, id, request.ExpectedRevision)
	if err != nil {
		// 删除失败时不能丢掉存储层已经读取到的标题，调用方可据此显示具体对象。
		return title, fmt.Errorf("delete schedule %d: %w", id, err)
	}
	return title, nil
}

// GetSchedule 读取单条日程。
func (s *Service) GetSchedule(ctx context.Context, id domain.ScheduleID) (ScheduleDTO, error) {
	if s.store == nil {
		return ScheduleDTO{}, errors.New("schedule store is not configured")
	}
	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("get schedule %d: %w", id, err)
	}
	// DTO 是内部状态到受控对外视图的投影，不暴露原始开始时间等内部字段。
	return scheduleDTO(schedule), nil
}

// deriveUpdateEndpoints 用保存的时长补齐更新请求缺失的一个时间端点。
func deriveUpdateEndpoints(schedule domain.Schedule, requestedStart, requestedEnd *int64) (int64, int64, error) {
	startUnix := schedule.FirstStartLocal.Unix()
	if schedule.DurationMinutes <= 0 || int64(schedule.DurationMinutes) > math.MaxInt64/60 {
		return 0, 0, domain.NewValidation("stored schedule duration is invalid", "duration_minutes")
	}
	durationSeconds := int64(schedule.DurationMinutes) * 60
	endUnix, overflow := addInt64(startUnix, durationSeconds)
	if overflow {
		return 0, 0, domain.NewValidation("stored schedule endpoint is out of range", "start_unix", "end_unix")
	}
	if requestedStart != nil {
		startUnix = *requestedStart
	}
	if requestedEnd != nil {
		endUnix = *requestedEnd
	} else if requestedStart != nil {
		var overflow bool
		endUnix, overflow = addInt64(startUnix, durationSeconds)
		if overflow {
			return 0, 0, domain.NewValidation("schedule end time is out of range", "start_unix", "end_unix")
		}
	}
	if requestedStart == nil && requestedEnd != nil {
		var overflow bool
		startUnix, overflow = addInt64(endUnix, -durationSeconds)
		if overflow {
			return 0, 0, domain.NewValidation("schedule start time is out of range", "start_unix", "end_unix")
		}
	}
	return startUnix, endUnix, nil
}

// addInt64 返回整数加法结果，并在边界溢出时报告失败。
func addInt64(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, true
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, true
	}
	return left + right, false
}

// ListSchedules 按游标分页读取日程。
func (s *Service) ListSchedules(ctx context.Context, request ListSchedulesRequest) (ListSchedulesResponse, error) {
	if s.store == nil {
		return ListSchedulesResponse{}, errors.New("schedule store is not configured")
	}
	if request.Limit < 0 {
		return ListSchedulesResponse{}, domain.NewValidation("limit must not be negative", "limit")
	}
	if request.Limit == 0 {
		request.Limit = defaultScheduleListLimit
	}
	// 列表只读取已经持久化的事实，不在分页过程中展开未来发生实例。
	return s.store.ListSchedules(ctx, request)
}

// GetScheduleTimeBounds 返回当前时间边界。
func (s *Service) GetScheduleTimeBounds(ctx context.Context) (TimeBoundsDTO, error) {
	if s.clock == nil {
		return TimeBoundsDTO{}, errors.New("schedule clock is not configured")
	}
	// 边界接口与创建接口一样只读取一次时钟，避免请求内跨越本地日期造成不一致。
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return TimeBoundsDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	return timeBoundsDTO(domain.CalculateTimeBounds(snapshot)), nil
}

// scheduleDTO 将领域日程转换为应用层的受控视图。
func scheduleDTO(schedule domain.Schedule) ScheduleDTO {
	return ScheduleDTO{
		ID:              int64(schedule.ID),
		Title:           schedule.Title,
		Cron:            schedule.Cron,
		Open:            schedule.Open,
		FirstStartLocal: domain.FormatLocalTime(schedule.FirstStartLocal),
		DurationMinutes: schedule.DurationMinutes,
		Revision:        schedule.Revision,
		CreatedAtUTC:    schedule.CreatedAtUTC,
		UpdatedAtUTC:    schedule.UpdatedAtUTC,
	}
}
