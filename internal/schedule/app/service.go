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
	store       ScheduleStore
	clock       Clock
	coordinator *MutationCoordinator
}

// NewService 创建日程应用服务。
func NewService(store ScheduleStore, clock Clock) *Service {
	return NewServiceWithCoordinator(store, clock, NewMutationCoordinator())
}

// NewServiceWithCoordinator 创建使用指定变更协调器的日程应用服务。
func NewServiceWithCoordinator(store ScheduleStore, clock Clock, coordinator *MutationCoordinator) *Service {
	if coordinator == nil {
		coordinator = NewMutationCoordinator()
	}
	return &Service{store: store, clock: clock, coordinator: coordinator}
}

// Coordinator 返回服务和提醒调度器共享的变更协调器。
func (s *Service) Coordinator() *MutationCoordinator {
	if s == nil {
		return nil
	}
	return s.coordinator
}

// CreateSchedule 校验首次发生和可选重复规则，再持久化日程定义。
func (s *Service) CreateSchedule(ctx context.Context, request CreateScheduleRequest) (ScheduleDTO, error) {
	if s.store == nil {
		return ScheduleDTO{}, errors.New("schedule store is not configured")
	}
	if s.clock == nil {
		return ScheduleDTO{}, errors.New("schedule clock is not configured")
	}
	// 每个请求只读取一次时间环境，确保边界计算和审计时间来自同一个快照。
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	candidate, err := prepareCreateCandidate(snapshot, request)
	if err != nil {
		return ScheduleDTO{}, err
	}

	// 领域校验是进入存储副作用前的最后守卫，非法请求不能调用写方法。
	s.coordinator.Lock()
	s.coordinator.Invalidate()
	schedule, err := s.store.CreateSchedule(ctx, candidate)
	s.coordinator.Unlock()
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("create schedule: %w", err)
	}
	result := scheduleDTO(schedule)
	operation := changeOperation{operation: ChangeOperationCreate, scheduleID: schedule.ID}
	conflict, err := s.committedConflict(ctx, snapshot, operation)
	if err != nil {
		return ScheduleDTO{}, err
	}
	result.Conflict = conflict
	return result, nil
}

// prepareCreateCandidate 校验创建请求并在无副作用的前提下生成日程候选。
// 创建用例和只读预览共用同一入口，避免预览放宽或偏离正式校验。
func prepareCreateCandidate(snapshot domain.TimeSnapshot, request CreateScheduleRequest) (domain.ScheduleCandidate, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return domain.ScheduleCandidate{}, domain.NewValidation("title must not be empty", "title")
	}
	if request.StartUnix == nil {
		return domain.ScheduleCandidate{}, domain.NewValidation("start time is required", "start_unix")
	}
	if request.EndUnix == nil {
		return domain.ScheduleCandidate{}, domain.NewValidation("end time is required", "end_unix")
	}
	if request.Cron == nil && (request.CronPresent || request.UntilUnixPresent || request.UntilUnix != nil) {
		return domain.ScheduleCandidate{}, domain.NewValidation("single schedule cannot carry recurrence fields", "cron", "until_unix")
	}
	if request.Cron != nil && request.UntilUnix == nil {
		return domain.ScheduleCandidate{}, domain.NewValidation("recurring cutoff is required", "until_unix")
	}
	var (
		candidate domain.ScheduleCandidate
		err       error
	)
	if request.Cron != nil {
		untilDate, dateErr := recurringUntilDate(snapshot.Location, *request.UntilUnix)
		if dateErr != nil {
			return domain.ScheduleCandidate{}, dateErr
		}
		candidate, err = domain.ValidateRecurring(snapshot, *request.StartUnix, *request.EndUnix, *request.Cron, untilDate)
	} else {
		candidate, err = domain.ValidateSingleEndpoints(snapshot, *request.StartUnix, *request.EndUnix)
	}
	if err != nil {
		return domain.ScheduleCandidate{}, err
	}
	candidate.Title = title
	candidate.Notes = request.Notes
	return candidate, nil
}

// UpdateSchedule 对日程应用字段补丁并使用版本号保护并发更新。
func (s *Service) UpdateSchedule(ctx context.Context, id domain.ScheduleID, request UpdateScheduleRequest) (ScheduleDTO, error) {
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
	if err := validateScheduleUpdateTarget(existing, request); err != nil {
		return ScheduleDTO{}, err
	}
	preservedOverrideCount := 0
	if existing.IsRecurring() {
		if occurrenceReader, supportsOccurrences := s.store.(OccurrenceReader); supportsOccurrences {
			overrides, readErr := occurrenceReader.ListOccurrenceOverrides(ctx, id)
			if readErr != nil {
				return ScheduleDTO{}, fmt.Errorf("read occurrence overrides for schedule %d: %w", id, readErr)
			}
			preservedOverrideCount = len(overrides)
		}
	}

	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	candidate, _, err := prepareScheduleUpdate(snapshot, existing, request)
	if err != nil {
		return ScheduleDTO{}, err
	}

	s.coordinator.Lock()
	s.coordinator.Invalidate()
	updated, err := updater.UpdateSchedule(ctx, id, candidate)
	s.coordinator.Unlock()
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("update schedule %d: %w", id, err)
	}
	result := scheduleDTO(updated)
	result.PreservedOverrideCount = preservedOverrideCount
	// 最终冲突必须基于提交后重新读取的事实和 generation，而不是预读或预览结果。
	operation := changeOperation{operation: ChangeOperationUpdateSchedule, scheduleID: id}
	conflict, err := s.committedConflict(ctx, snapshot, operation)
	if err != nil {
		return ScheduleDTO{}, err
	}
	result.Conflict = conflict
	return result, nil
}

// validateScheduleUpdateTarget 在读取时间快照前拒绝旧版本和类型不匹配的整组编辑。
func validateScheduleUpdateTarget(existing domain.Schedule, request UpdateScheduleRequest) error {
	if request.ExpectedRevision <= 0 {
		return domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	if existing.Revision != request.ExpectedRevision {
		// 预读发现旧版本时不读取时间也不生成补丁；事务内仍会复查以防预读后的竞争。
		return domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}
	if !existing.IsRecurring() && (request.CronPresent || request.Cron != nil || request.UntilUnixPresent || request.UntilUnix != nil) {
		return domain.NewValidation("single schedule cannot carry recurrence fields", "cron", "until_unix")
	}
	return nil
}

// prepareScheduleUpdate 校验整组编辑并用一次快照生成存储补丁和变更后的定义。
// 正式更新和只读预览共用同一入口，确保预览不会绕过任何业务校验。
func prepareScheduleUpdate(snapshot domain.TimeSnapshot, existing domain.Schedule, request UpdateScheduleRequest) (domain.ScheduleUpdateCandidate, domain.Schedule, error) {
	if err := validateScheduleUpdateTarget(existing, request); err != nil {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, err
	}
	if request.CancelPresent || request.Cancel != nil {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("cancel is not applicable to schedule update", "cancel")
	}
	if request.RestorePresent || request.Restore != nil {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("restore is not applicable to schedule update", "restore")
	}

	titlePresent := request.TitlePresent || request.Title != nil
	notesPresent := request.NotesPresent || request.Notes != nil
	startPresent := request.StartUnixPresent || request.StartUnix != nil
	endPresent := request.EndUnixPresent || request.EndUnix != nil
	cronPresent := request.CronPresent || request.Cron != nil
	untilPresent := request.UntilUnixPresent || request.UntilUnix != nil
	if !titlePresent && !notesPresent && !startPresent && !endPresent && !cronPresent && !untilPresent {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("at least one schedule field must be provided", "title", "notes", "start_unix", "end_unix")
	}
	if titlePresent {
		if request.Title == nil {
			return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("title must be a string", "title")
		}
		if strings.TrimSpace(*request.Title) == "" {
			return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("title must not be empty", "title")
		}
	}
	if notesPresent && request.Notes == nil {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("notes must be a string", "notes")
	}
	if startPresent && request.StartUnix == nil {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("start time must be an integer", "start_unix")
	}
	if endPresent && request.EndUnix == nil {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("end time must be an integer", "end_unix")
	}
	if cronPresent && request.Cron == nil {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("cron cannot be cleared", "cron")
	}
	if untilPresent && request.UntilUnix == nil {
		return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, domain.NewValidation("cutoff cannot be cleared", "until_unix")
	}

	candidate := domain.ScheduleUpdateCandidate{
		ExpectedRevision: request.ExpectedRevision,
		UpdatedAtUTC:     snapshot.Now.UTC(),
	}
	// 变更后的定义用于预览冲突：它必须应用与存储补丁相同的字段，并递增版本。
	post := existing
	post.Revision = existing.Revision + 1
	post.UpdatedAtUTC = snapshot.Now.UTC()
	if titlePresent {
		title := strings.TrimSpace(*request.Title)
		candidate.Title = &title
		post.Title = title
	}
	if notesPresent {
		notes := *request.Notes
		candidate.Notes = &notes
		post.Notes = notes
	}
	if existing.IsRecurring() {
		if err := prepareRecurringUpdate(snapshot, existing, request, &candidate); err != nil {
			return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, err
		}
		applyRecurringPostChange(candidate, &post)
	} else if startPresent || endPresent {
		startUnix, endUnix, err := deriveUpdateEndpoints(existing, request.StartUnix, request.EndUnix)
		if err != nil {
			return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, err
		}
		validated, err := domain.ValidateSingleEndpoints(snapshot, startUnix, endUnix)
		if err != nil {
			return domain.ScheduleUpdateCandidate{}, domain.Schedule{}, err
		}
		candidate.FirstStartLocal = &validated.FirstStartLocal
		candidate.DurationMinutes = &validated.DurationMinutes
		post.FirstStartLocal = validated.FirstStartLocal
		post.DurationMinutes = validated.DurationMinutes
	}
	return candidate, post, nil
}

// applyRecurringPostChange 把重复编辑补丁投影到变更后的定义，供只读预览计算使用。
func applyRecurringPostChange(candidate domain.ScheduleUpdateCandidate, post *domain.Schedule) {
	if candidate.CronPresent || candidate.Cron != nil {
		post.Cron = candidate.Cron
	}
	if candidate.MonthEndPolicyPresent || candidate.MonthEndPolicy != nil {
		post.MonthEndPolicy = candidate.MonthEndPolicy
	}
	if candidate.UntilDateLocal != nil {
		post.UntilDateLocal = candidate.UntilDateLocal
	}
	if candidate.FirstStartLocal != nil {
		post.FirstStartLocal = *candidate.FirstStartLocal
	}
	if candidate.DurationMinutes != nil {
		post.DurationMinutes = *candidate.DurationMinutes
	}
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
	s.coordinator.Lock()
	s.coordinator.Invalidate()
	title, err := deleter.DeleteSchedule(ctx, id, request.ExpectedRevision)
	s.coordinator.Unlock()
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
	result := scheduleDTO(schedule)
	reader, ok := s.store.(OccurrenceReader)
	if !ok {
		return ScheduleDTO{}, errors.New("schedule store does not support occurrence reads")
	}
	overrides, err := reader.ListOccurrenceOverrides(ctx, id)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("read occurrence overrides for schedule %d: %w", id, err)
	}
	result.CancelledOccurrences = cancelledOccurrenceDTOs(overrides)
	return result, nil
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
		Notes:           schedule.Notes,
		Cron:            schedule.Cron,
		UntilDateLocal:  schedule.UntilDateLocal,
		MonthEndPolicy:  schedule.MonthEndPolicy,
		Open:            schedule.Open,
		FirstStartLocal: domain.FormatLocalTime(schedule.FirstStartLocal),
		DurationMinutes: schedule.DurationMinutes,
		Revision:        schedule.Revision,
		CreatedAtUTC:    schedule.CreatedAtUTC,
		UpdatedAtUTC:    schedule.UpdatedAtUTC,
	}
}

// cancelledOccurrenceDTOs 将持久化取消事实投影为稳定顺序的受控列表。
func cancelledOccurrenceDTOs(overrides []domain.OccurrenceOverride) []CancelledOccurrenceDTO {
	result := make([]CancelledOccurrenceDTO, 0)
	for _, override := range overrides {
		if override.Open {
			continue
		}
		result = append(result, cancelledOccurrenceDTO(override))
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// cancelledOccurrenceDTO 将单个取消例外转换为不暴露内部时间对象的视图。
func cancelledOccurrenceDTO(override domain.OccurrenceOverride) CancelledOccurrenceDTO {
	result := CancelledOccurrenceDTO{
		OriginalDate:    override.OriginalDate,
		SnapshotPresent: override.SnapshotPresent,
	}
	if !override.SnapshotPresent {
		return result
	}
	startUnix := override.StartLocal.Unix()
	endUnix := override.StartLocal.Add(duration(override.DurationMinutes)).Unix()
	title, notes := override.Title, override.Notes
	result.StartUnix = &startUnix
	result.EndUnix = &endUnix
	result.Title = &title
	result.Notes = &notes
	return result
}
