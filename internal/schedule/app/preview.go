package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// 冲突检查状态常量与领域保持一致，作为受控对外的机器可读取值。
const (
	conflictStatusChecked    = "CHECKED"
	conflictStatusNoTarget   = "NO_FUTURE_OCCURRENCE"
	conflictStatusPastTarget = "PAST_OCCURRENCE"
	conflictStatusClosed     = "SCHEDULE_CLOSED"
)

// 未检查原因常量说明为什么本次没有比较任何发生。
const (
	conflictReasonNoOccurrence  = "operation_produces_no_occurrence_after_now"
	conflictReasonPast          = "target_occurrence_is_in_the_past"
	conflictReasonScheduleClose = "schedule_is_closed"
)

// changeOperation 保存一次已校验的预览操作及其变更后的投影。
// 它只承载只读计算所需的输入，不包含任何写端口。
type changeOperation struct {
	operation    string
	scheduleID   domain.ScheduleID
	originalDate string

	// post 是应用变更后的日程定义，overlay 标记目标日程需要替换读取结果。
	post    domain.Schedule
	overlay bool
	// closed 表示变更后的父组仍然关闭，此时不存在可检查的发生。
	closed bool

	// candidateSchedule 和 candidateOccurrence 只在编辑操作时非空，供提交复用同一校验结果。
	candidateSchedule   *domain.ScheduleUpdateCandidate
	candidateOccurrence *domain.OccurrenceOverrideCandidate

	// singleStart 非零时表示本次检查只针对该实际开始时刻的单次发生。
	singleStart time.Time
}

// conflictDetection 保存一次冲突检查的对外结果和是否存在后续发生。
type conflictDetection struct {
	Check   domain.ConflictCheck
	Target  domain.Occurrence
	HasMore bool
}

// PreviewChange 只读预览一次新建、整组编辑/恢复或单次编辑/恢复后的下一次冲突。
// 它复用正式业务校验并只调用查询端口，绝不产生任何写入副作用。
func (s *Service) PreviewChange(ctx context.Context, request PreviewChangeRequest) (ChangePreviewDTO, error) {
	if s.store == nil {
		return ChangePreviewDTO{}, errors.New("schedule store is not configured")
	}
	if _, ok := s.store.(OccurrenceQueryStore); !ok {
		return ChangePreviewDTO{}, errors.New("schedule store does not support occurrence queries")
	}
	if s.clock == nil {
		return ChangePreviewDTO{}, errors.New("schedule clock is not configured")
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ChangePreviewDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	operation, err := s.resolveChangeOperation(ctx, snapshot, request)
	if err != nil {
		return ChangePreviewDTO{}, err
	}

	detection, err := s.detectChangeConflicts(ctx, snapshot, operation)
	if err != nil {
		return ChangePreviewDTO{}, err
	}
	conflict := conflictCheckDTO(detection.Check, detection.Target)
	return ChangePreviewDTO{
		Operation: operation.operation,
		Conflict:  conflict,
		Checked:   conflict.Checked,
		HasMore:   detection.HasMore,
	}, nil
}

// resolveChangeOperation 校验操作类型和必要载荷，并计算变更后的日程投影。
// 所有分支都必须先完成正式业务校验，任何非法请求都不会触碰查询或写入端口。
func (s *Service) resolveChangeOperation(ctx context.Context, snapshot domain.TimeSnapshot, request PreviewChangeRequest) (changeOperation, error) {
	operation := strings.TrimSpace(request.Operation)
	switch operation {
	case ChangeOperationCreate, ChangeOperationUpdateSchedule, ChangeOperationRestoreSchedule,
		ChangeOperationUpdateOccurrence, ChangeOperationRestoreOccurrence:
	case "":
		return changeOperation{}, domain.NewValidation("operation is required", "operation")
	default:
		return changeOperation{}, domain.NewValidation("operation is not supported", "operation")
	}

	scheduleID := domain.ScheduleID(request.ScheduleID)
	if operation == ChangeOperationCreate {
		// 新建预览不能携带既有日程字段，避免把两种语义混在同一次请求里。
		if request.ScheduleIDPresent || scheduleID != 0 || request.OriginalDatePresent || request.ExpectedRevisionPresent {
			return changeOperation{}, domain.NewValidation("create preview cannot carry schedule identity fields", "schedule_id", "original_date", "expected_revision")
		}
		if request.ScheduleUpdate != nil || request.OccurrenceUpdate != nil || request.OccurrenceAction != nil || request.ScheduleAction != nil {
			return changeOperation{}, domain.NewValidation("create preview cannot carry edit payloads", "schedule_update", "occurrence_update", "occurrence_action", "schedule_action")
		}
		if request.Create == nil {
			return changeOperation{}, domain.NewValidation("create payload is required", "create")
		}
		candidate, err := prepareCreateCandidate(snapshot, *request.Create)
		if err != nil {
			return changeOperation{}, err
		}
		return changeOperation{
			operation:  operation,
			scheduleID: pendingScheduleID,
			post:       pendingSchedule(candidate),
			// 新建日程尚未持久化，必须显式加入查询源投影。
			overlay: true,
			closed:  !candidate.Open,
		}, nil
	}

	if !request.ScheduleIDPresent || scheduleID <= 0 {
		return changeOperation{}, domain.NewValidation("schedule ID must be positive", "schedule_id")
	}
	if !request.ExpectedRevisionPresent || request.ExpectedRevision <= 0 {
		return changeOperation{}, domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	single := operation == ChangeOperationUpdateOccurrence || operation == ChangeOperationRestoreOccurrence
	if single && strings.TrimSpace(request.OriginalDate) == "" {
		return changeOperation{}, domain.NewValidation("original date is required", "original_date")
	}
	if !single && request.OriginalDatePresent {
		return changeOperation{}, domain.NewValidation("original date is only valid for occurrence operations", "original_date")
	}
	if request.Create != nil {
		return changeOperation{}, domain.NewValidation("create payload is not valid for this operation", "create")
	}

	switch operation {
	case ChangeOperationUpdateSchedule:
		return s.resolvePreviewScheduleUpdate(ctx, snapshot, scheduleID, request)
	case ChangeOperationRestoreSchedule:
		return s.resolvePreviewScheduleRestore(ctx, scheduleID, request)
	case ChangeOperationUpdateOccurrence:
		return s.resolvePreviewOccurrenceUpdate(ctx, snapshot, scheduleID, request)
	default:
		return s.resolvePreviewOccurrenceRestore(ctx, snapshot, scheduleID, request)
	}
}

// resolvePreviewScheduleUpdate 用与正式更新相同的校验计算整组编辑后的定义。
func (s *Service) resolvePreviewScheduleUpdate(ctx context.Context, snapshot domain.TimeSnapshot, id domain.ScheduleID, request PreviewChangeRequest) (changeOperation, error) {
	if request.ScheduleUpdate == nil {
		return changeOperation{}, domain.NewValidation("schedule update payload is required", "schedule_update")
	}
	if request.OccurrenceUpdate != nil || request.OccurrenceAction != nil || request.ScheduleAction != nil {
		return changeOperation{}, domain.NewValidation("unexpected edit payload for schedule update", "occurrence_update", "occurrence_action", "schedule_action")
	}
	payload := *request.ScheduleUpdate
	payload.ExpectedRevision = request.ExpectedRevision
	existing, err := s.previewSchedule(ctx, id)
	if err != nil {
		return changeOperation{}, err
	}
	candidate, post, err := prepareScheduleUpdate(snapshot, existing, payload)
	if err != nil {
		return changeOperation{}, err
	}
	return changeOperation{
		operation: ChangeOperationUpdateSchedule, scheduleID: id,
		post: post, overlay: true, closed: !post.Open, candidateSchedule: &candidate,
	}, nil
}

// resolvePreviewScheduleRestore 校验整组恢复，并把变更后的定义投影为重新开放。
func (s *Service) resolvePreviewScheduleRestore(ctx context.Context, id domain.ScheduleID, request PreviewChangeRequest) (changeOperation, error) {
	if request.ScheduleUpdate != nil || request.OccurrenceUpdate != nil || request.OccurrenceAction != nil {
		return changeOperation{}, domain.NewValidation("unexpected edit payload for schedule restoration", "schedule_update", "occurrence_update", "occurrence_action")
	}
	if request.ScheduleAction != nil &&
		(request.ScheduleAction.ExpectedRevisionPresent || request.ScheduleAction.ExpectedRevision != 0) &&
		request.ScheduleAction.ExpectedRevision != request.ExpectedRevision {
		return changeOperation{}, domain.NewValidation("expected revision must match the preview request", "expected_revision")
	}
	existing, err := s.previewSchedule(ctx, id)
	if err != nil {
		return changeOperation{}, err
	}
	if !existing.IsRecurring() {
		return changeOperation{}, domain.NewValidation("schedule restoration requires a recurring schedule", "id")
	}
	if existing.Revision != request.ExpectedRevision {
		return changeOperation{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}
	post := existing
	// 整组恢复只改变开放状态；已经开放时不产生新的版本语义。
	if !post.Open {
		post.Open = true
		post.Revision = existing.Revision + 1
	}
	return changeOperation{
		operation: ChangeOperationRestoreSchedule, scheduleID: id,
		post: post, overlay: true,
	}, nil
}

// resolvePreviewOccurrenceUpdate 用与正式单次编辑相同的校验计算变更后的发生。
func (s *Service) resolvePreviewOccurrenceUpdate(ctx context.Context, snapshot domain.TimeSnapshot, id domain.ScheduleID, request PreviewChangeRequest) (changeOperation, error) {
	if request.OccurrenceUpdate == nil {
		return changeOperation{}, domain.NewValidation("occurrence update payload is required", "occurrence_update")
	}
	if request.ScheduleUpdate != nil || request.OccurrenceAction != nil || request.ScheduleAction != nil {
		return changeOperation{}, domain.NewValidation("unexpected edit payload for occurrence update", "schedule_update", "occurrence_action", "schedule_action")
	}
	payload := *request.OccurrenceUpdate
	payload.OriginalDate = request.OriginalDate
	payload.ExpectedRevision = request.ExpectedRevision
	resolved, err := resolveOccurrenceUpdate(ctx, s.store, snapshot, id, payload)
	if err != nil {
		return changeOperation{}, err
	}
	candidate := resolved.Candidate
	return changeOperation{
		operation: ChangeOperationUpdateOccurrence, scheduleID: id, originalDate: request.OriginalDate,
		post: resolved.Schedule, overlay: true, candidateOccurrence: &candidate,
		// 父组关闭时即使例外快照仍存在，变更后也不会产生任何发生。
		closed:      !resolved.Schedule.Open,
		singleStart: resolved.Post.StartLocal,
	}, nil
}

// resolvePreviewOccurrenceRestore 校验单次恢复，并把变更后的例外投影为开放状态。
func (s *Service) resolvePreviewOccurrenceRestore(ctx context.Context, snapshot domain.TimeSnapshot, id domain.ScheduleID, request PreviewChangeRequest) (changeOperation, error) {
	if request.OccurrenceUpdate != nil || request.ScheduleUpdate != nil || request.ScheduleAction != nil {
		return changeOperation{}, domain.NewValidation("unexpected edit payload for occurrence restoration", "schedule_update", "occurrence_update", "schedule_action")
	}
	if request.OccurrenceAction != nil {
		// 单次操作的身份和版本必须来自顶层字段，避免载荷与顶层出现两套不一致的语义。
		if request.OccurrenceAction.OriginalDate != "" && request.OccurrenceAction.OriginalDate != request.OriginalDate {
			return changeOperation{}, domain.NewValidation("original date must match the preview request", "original_date")
		}
		if request.OccurrenceAction.ExpectedRevision != 0 && request.OccurrenceAction.ExpectedRevision != request.ExpectedRevision {
			return changeOperation{}, domain.NewValidation("expected revision must match the preview request", "expected_revision")
		}
	}
	existing, err := s.previewSchedule(ctx, id)
	if err != nil {
		return changeOperation{}, err
	}
	if existing.Revision != request.ExpectedRevision {
		return changeOperation{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}
	if !existing.Open {
		// 父组关闭时恢复整组之前的快照不产生发生，必须明确报告未检查。
		return changeOperation{
			operation: ChangeOperationRestoreOccurrence, scheduleID: id, originalDate: request.OriginalDate,
			post: existing, overlay: true, closed: true,
		}, nil
	}
	reader, ok := s.store.(OccurrenceReader)
	if !ok {
		return changeOperation{}, errors.New("schedule store does not support occurrence reads")
	}
	override, overrideErr := reader.GetOccurrenceOverride(ctx, id, request.OriginalDate)
	if overrideErr != nil && !isNotFound(overrideErr) {
		return changeOperation{}, fmt.Errorf("read occurrence override %d/%s: %w", id, request.OriginalDate, overrideErr)
	}

	var singleAt time.Time
	switch {
	case overrideErr != nil:
		// 没有取消记录时恢复是幂等操作，但当前规则必须仍然产生该身份。
		current, err := requireOccurrenceOnDate(existing, request.OriginalDate, snapshot.Location)
		if err != nil {
			return changeOperation{}, err
		}
		singleAt = current.StartLocal
	case !override.Open && !override.SnapshotPresent:
		// 空快照取消只有在当前规则重新产生该身份时才能被移除。
		current, err := requireOccurrenceOnDate(existing, request.OriginalDate, snapshot.Location)
		if err != nil {
			return changeOperation{}, err
		}
		singleAt = current.StartLocal
	default:
		singleAt = override.StartLocal
	}
	return changeOperation{
		operation: ChangeOperationRestoreOccurrence, scheduleID: id, originalDate: request.OriginalDate,
		post: existing, overlay: true, singleStart: singleAt,
	}, nil
}

// previewSchedule 通过日程读取端口预读目标日程，预览不依赖任何写能力。
func (s *Service) previewSchedule(ctx context.Context, id domain.ScheduleID) (domain.Schedule, error) {
	existing, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return domain.Schedule{}, fmt.Errorf("read schedule %d for preview: %w", id, err)
	}
	return existing, nil
}

// requireOccurrenceOnDate 读取指定原始日期的当前发生，不存在时返回校验错误。
func requireOccurrenceOnDate(schedule domain.Schedule, originalDate string, location *time.Location) (domain.Occurrence, error) {
	occurrence, found, err := domain.OccurrenceOnDate(schedule, originalDate, location)
	if err != nil {
		return domain.Occurrence{}, err
	}
	if !found {
		return domain.Occurrence{}, domain.NewValidation("original date does not belong to the current schedule", "original_date")
	}
	return occurrence, nil
}

// committedConflict 在业务写入成功提交后重新读取已提交事实，并计算返回给调用方的最终冲突。
// 预览、预读和候选都不能作为最终结果来源；读取失败必须向上返回，不能退化成“无冲突”。
func (s *Service) committedConflict(ctx context.Context, snapshot domain.TimeSnapshot, operation changeOperation) (*ConflictCheckDTO, error) {
	queryStore, ok := s.store.(OccurrenceQueryStore)
	if !ok {
		// 存储不支持发生查询时跳过冲突结果，但不影响已经提交成功的业务事实。
		return nil, nil
	}
	if snapshot.Location == nil {
		return nil, domain.NewValidation("time location must not be nil", "location")
	}
	committed, err := s.store.GetSchedule(ctx, operation.scheduleID)
	if err != nil {
		return nil, fmt.Errorf("read committed schedule %d for conflict: %w", operation.scheduleID, err)
	}
	if !committed.Open {
		// 关闭组不产生任何发生，恢复前的快照不能表示整个系列永远无冲突。
		dto := conflictCheckDTO(domain.ConflictCheck{Status: domain.ConflictClosedGroup}, domain.Occurrence{})
		return &dto, nil
	}
	sources, err := s.loadConflictSources(ctx, queryStore, changeOperation{})
	if err != nil {
		return nil, err
	}
	target, found, err := committedConflictTarget(sources, operation, snapshot, snapshot.Location)
	if err != nil {
		return nil, err
	}
	if !found {
		// 写入成功后仍然没有可检查的发生时，必须报告本次未检查。
		status := domain.ConflictNoTarget
		if !operation.singleStart.IsZero() && !operation.singleStart.After(snapshot.Now) {
			status = domain.ConflictPastTarget
		}
		dto := conflictCheckDTO(domain.ConflictCheck{Status: status}, domain.Occurrence{})
		return &dto, nil
	}
	check, err := s.detectCommittedConflicts(ctx, sources, target, snapshot.Location)
	if err != nil {
		return nil, err
	}
	dto := conflictCheckDTO(check, target)
	return &dto, nil
}

// detectCommittedConflicts 按已提交目标的实际时刻比较全部日程定义，并校验查询快照未变化。
func (s *Service) detectCommittedConflicts(ctx context.Context, sources domain.ConflictSources, target domain.Occurrence, location *time.Location) (domain.ConflictCheck, error) {
	queryStore, ok := s.store.(OccurrenceQueryStore)
	if !ok {
		return domain.ConflictCheck{}, errors.New("schedule store does not support occurrence queries")
	}
	generation, err := queryStore.QueryGeneration(ctx)
	if err != nil {
		return domain.ConflictCheck{}, fmt.Errorf("read occurrence query generation for conflict: %w", err)
	}
	start, end := conflictTargetWindow([]domain.Occurrence{target})
	check, err := domain.DetectOccurrenceConflicts(sources, []domain.Occurrence{target}, start, end, sourceLookback(sources), location)
	if err != nil {
		return domain.ConflictCheck{}, fmt.Errorf("detect committed occurrence conflicts: %w", err)
	}
	// 读取过程中事实发生变化时必须报错，避免返回与已提交 generation 不一致的结果。
	if err := ensureOccurrenceGeneration(ctx, queryStore, generation); err != nil {
		return domain.ConflictCheck{}, err
	}
	return check, nil
}

// committedConflictTarget 从提交后的事实中确定本次操作实际检查的发生。
func committedConflictTarget(sources domain.ConflictSources, operation changeOperation, snapshot domain.TimeSnapshot, location *time.Location) (domain.Occurrence, bool, error) {
	for _, source := range sources.Items {
		if source.Schedule.ID != operation.scheduleID {
			continue
		}
		if operation.originalDate == "" {
			// 整组操作检查提交后该日程严格晚于当前时刻的最早发生。
			targets, err := domain.SelectNextTargetBatch(domain.ConflictSources{Items: []domain.ConflictSource{source}}, snapshot.Now.Add(-time.Second), location)
			if err != nil {
				return domain.Occurrence{}, false, fmt.Errorf("select committed target batch: %w", err)
			}
			if len(targets) == 0 {
				return domain.Occurrence{}, false, nil
			}
			return targets[0], true, nil
		}
		// 单次操作只检查提交后的指定身份，不自动改查其他发生。
		for _, override := range source.Overrides {
			if override.OriginalDate != operation.originalDate || !override.Open || !override.SnapshotPresent {
				continue
			}
			return domain.Occurrence{
				ScheduleID:   source.Schedule.ID,
				OriginalDate: override.OriginalDate,
				StartLocal:   override.StartLocal,
				EndLocal:     override.StartLocal.Add(time.Duration(override.DurationMinutes) * time.Minute),
				Title:        override.Title,
				Notes:        override.Notes,
			}, true, nil
		}
		occurrence, found, err := domain.OccurrenceOnDate(source.Schedule, operation.originalDate, location)
		if err != nil {
			return domain.Occurrence{}, false, err
		}
		if found {
			return occurrence, true, nil
		}
	}
	return domain.Occurrence{}, false, nil
}

// detectChangeConflicts 基于变更后的投影选择目标批次并计算最终冲突。
// 它只读取查询源快照，不做任何写入，也不使用公开查询的未来上界。
func (s *Service) detectChangeConflicts(ctx context.Context, snapshot domain.TimeSnapshot, operation changeOperation) (conflictDetection, error) {
	queryStore, ok := s.store.(OccurrenceQueryStore)
	if !ok {
		return conflictDetection{}, errors.New("schedule store does not support occurrence queries")
	}
	if snapshot.Location == nil {
		return conflictDetection{}, domain.NewValidation("time location must not be nil", "location")
	}
	if operation.closed {
		// 关闭组不产生任何发生，不能用空冲突表示“整个系列永远无冲突”。
		return conflictDetection{Check: domain.ConflictCheck{Status: domain.ConflictClosedGroup}}, nil
	}
	if !operation.singleStart.IsZero() && !operation.singleStart.After(snapshot.Now) {
		// 单次操作的目标已经在过去时必须报告本次未检查，不能改查其他发生。
		return conflictDetection{Check: domain.ConflictCheck{Status: domain.ConflictPastTarget}}, nil
	}
	sources, err := s.loadConflictSources(ctx, queryStore, operation)
	if err != nil {
		return conflictDetection{}, err
	}

	// 选择最早批次时使用包含式下界，避免漏掉与 now 同刻的发生。
	from := snapshot.Now.Add(-time.Second)
	if !operation.singleStart.IsZero() {
		from = operation.singleStart.Add(-time.Second)
	}
	targets, err := s.conflictTargetBatch(sources, operation, from, snapshot.Location)
	if err != nil {
		return conflictDetection{}, err
	}
	if len(targets) == 0 {
		// 没有未来候选时必须区分“变更后不再发生”和“指定发生已经在过去”。
		if !operation.singleStart.IsZero() && !operation.singleStart.After(snapshot.Now) {
			return conflictDetection{Check: domain.ConflictCheck{Status: domain.ConflictPastTarget}}, nil
		}
		return conflictDetection{Check: domain.ConflictCheck{Status: domain.ConflictNoTarget}}, nil
	}

	start, end := conflictTargetWindow(targets)
	// 目标批次只属于本次操作的日程，但比较必须覆盖全部日程定义。
	check, err := domain.DetectOccurrenceConflicts(sources, targets, start, end, sourceLookback(sources), snapshot.Location)
	if err != nil {
		return conflictDetection{}, fmt.Errorf("detect occurrence conflicts: %w", err)
	}
	hasMore, err := hasOccurrenceAfterBatch(sources, end, snapshot.Location)
	if err != nil {
		return conflictDetection{}, fmt.Errorf("probe following occurrences: %w", err)
	}
	return conflictDetection{Check: check, Target: operationTarget(targets, operation), HasMore: hasMore}, nil
}

// operationTarget 返回本次操作自身在批次中的发生，用于对外说明检查目标。
func operationTarget(targets []domain.Occurrence, operation changeOperation) domain.Occurrence {
	for _, target := range targets {
		if target.ScheduleID == operation.scheduleID &&
			(operation.originalDate == "" || target.OriginalDate == operation.originalDate) {
			return target
		}
	}
	if len(targets) > 0 {
		return targets[0]
	}
	return domain.Occurrence{}
}

// loadConflictSources 读取全部日程定义，并在目标日程上叠加变更后的投影。
func (s *Service) loadConflictSources(ctx context.Context, queryStore OccurrenceQueryStore, operation changeOperation) (domain.ConflictSources, error) {
	sources := domain.ConflictSources{}
	var cursor domain.ScheduleID
	replaced := false
	for {
		batch, err := queryStore.ListOccurrenceSources(ctx, ListOccurrenceSourcesRequest{
			AfterScheduleID: cursor, Limit: occurrenceSourceBatchLimit,
		})
		if err != nil {
			return domain.ConflictSources{}, fmt.Errorf("list conflict sources after schedule %d: %w", cursor, err)
		}
		for _, item := range batch.Items {
			source := domain.ConflictSource{Schedule: item.Schedule, Overrides: item.Overrides}
			if operation.overlay && item.Schedule.ID == operation.scheduleID {
				source.Schedule = operation.post
				source.Overrides = applyChangeOverrides(item.Overrides, operation)
				replaced = true
			}
			sources.Items = append(sources.Items, source)
		}
		if batch.NextCursor == 0 {
			break
		}
		if batch.NextCursor <= cursor {
			return domain.ConflictSources{}, errors.New("conflict source cursor did not advance")
		}
		cursor = batch.NextCursor
	}
	if operation.overlay && !replaced {
		// 新建预览的日程尚未持久化，必须显式加入变更后的投影。
		sources.Items = append(sources.Items, domain.ConflictSource{
			Schedule: operation.post, Overrides: applyChangeOverrides(nil, operation),
		})
	}
	return sources, nil
}

// applyChangeOverrides 在目标日程上叠加本次变更产生的例外投影。
func applyChangeOverrides(overrides []domain.OccurrenceOverride, operation changeOperation) []domain.OccurrenceOverride {
	if operation.candidateOccurrence != nil {
		candidate := *operation.candidateOccurrence
		return postChangeOverrides(overrides, domain.OccurrenceOverride{
			ScheduleID:      operation.scheduleID,
			OriginalDate:    candidate.OriginalDate,
			StartLocal:      candidate.StartLocal,
			DurationMinutes: candidate.DurationMinutes,
			Title:           candidate.Title,
			Notes:           candidate.Notes,
			Open:            true, SnapshotPresent: true,
		})
	}
	if operation.originalDate == "" {
		return overrides
	}
	// 单次恢复把该身份的取消例外投影为开放；空快照取消在规则重新产生身份时被移除。
	for _, override := range overrides {
		if override.OriginalDate != operation.originalDate {
			continue
		}
		if !override.SnapshotPresent {
			return removeOverride(overrides, operation.originalDate)
		}
		restored := override
		restored.Open = true
		return replaceOverride(overrides, restored)
	}
	return overrides
}

// conflictTargetBatch 选择本次操作实际检查的目标批次。
// 单次操作和整组操作都从操作自身日程的变更后定义出发，再按同刻身份组成批次；
// 其他日程只作为冲突方参与比较，不参与目标选择。
func (s *Service) conflictTargetBatch(sources domain.ConflictSources, operation changeOperation, from time.Time, location *time.Location) ([]domain.Occurrence, error) {
	if operation.scheduleID != pendingScheduleID && operation.scheduleID <= 0 {
		// 非新建操作必须携带目标日程标识，缺失时无法确定检查目标。
		return nil, domain.NewValidation("schedule ID must be positive", "schedule_id")
	}
	scoped := domain.ConflictSources{}
	for _, source := range sources.Items {
		if source.Schedule.ID == operation.scheduleID {
			scoped.Items = append(scoped.Items, source)
		}
	}
	targets, err := domain.SelectNextTargetBatch(scoped, from, location)
	if err != nil {
		return nil, fmt.Errorf("select conflict target batch: %w", err)
	}
	return filterConflictTargets(targets, operation), nil
}

// filterConflictTargets 把批次裁剪为本次操作实际检查的身份。
func filterConflictTargets(targets []domain.Occurrence, operation changeOperation) []domain.Occurrence {
	if operation.originalDate == "" {
		return targets
	}
	for _, target := range targets {
		if target.OriginalDate == operation.originalDate {
			// 单次操作只检查指定的发生，不自动改查同批次的其它身份。
			return []domain.Occurrence{target}
		}
	}
	return nil
}

// conflictTargetWindow 返回覆盖整个批次的最早开始和最晚结束时间。
func conflictTargetWindow(targets []domain.Occurrence) (time.Time, time.Time) {
	start, end := targets[0].StartLocal, targets[0].EndLocal
	for _, target := range targets[1:] {
		if target.StartLocal.Before(start) {
			start = target.StartLocal
		}
		if target.EndLocal.After(end) {
			end = target.EndLocal
		}
	}
	return start, end
}

// sourceLookback 返回全部日程和例外中最长的持续时间，用于覆盖跨天实例。
func sourceLookback(sources domain.ConflictSources) time.Duration {
	minutes := 0
	for _, source := range sources.Items {
		if source.Schedule.DurationMinutes > minutes {
			minutes = source.Schedule.DurationMinutes
		}
		for _, override := range source.Overrides {
			if override.DurationMinutes > minutes {
				minutes = override.DurationMinutes
			}
		}
	}
	if minutes <= 0 || int64(minutes) > math.MaxInt64/int64(time.Minute) {
		return 0
	}
	return time.Duration(minutes) * time.Minute
}

// hasOccurrenceAfterBatch 判断批次之后是否仍有后续发生，供预览提示本次只检查最近批次。
func hasOccurrenceAfterBatch(sources domain.ConflictSources, batchEnd time.Time, location *time.Location) (bool, error) {
	for _, source := range sources.Items {
		_, ok, err := domain.NextMergedOccurrence(source.Schedule, source.Overrides, batchEnd, location)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// conflictCheckDTO 把领域冲突结果投影为受控视图，未检查时给出机器可读原因。
func conflictCheckDTO(check domain.ConflictCheck, target domain.Occurrence) ConflictCheckDTO {
	result := ConflictCheckDTO{
		Status:           string(check.Status),
		Checked:          check.Status == domain.ConflictChecked,
		BatchUnix:        check.TargetUnix,
		OccurrenceCount:  len(check.Targets),
		SameScheduleOnly: check.SameScheduleOnly,
		Conflicts:        conflictDTOs(check.Conflicts),
	}
	if !result.Checked {
		result.Reason = conflictReason(check.Status)
		result.BatchUnix = 0
		result.SameScheduleOnly = false
		return result
	}
	if len(check.Targets) > 0 {
		identity := OccurrenceIdentityDTO{
			ScheduleID:   int64(check.Targets[0].ScheduleID),
			OriginalDate: check.Targets[0].OriginalDate,
			StartUnix:    check.Targets[0].StartUnix,
			Title:        target.Title,
			EndUnix:      target.EndLocal.Unix(),
		}
		// 操作自身在批次中的发生优先作为对外目标身份。
		if !target.StartLocal.IsZero() {
			identity = occurrenceIdentityDTO(domain.ConflictOccurrence{
				ScheduleID: target.ScheduleID, OriginalDate: target.OriginalDate, Title: target.Title,
				StartUnix: target.StartLocal.Unix(), EndUnix: target.EndLocal.Unix(),
			})
		}
		result.Target = &identity
	}
	return result
}

// conflictReason 为未执行检查的状态返回稳定的机器可读原因。
func conflictReason(status domain.ConflictStatus) string {
	switch status {
	case domain.ConflictNoTarget:
		return conflictReasonNoOccurrence
	case domain.ConflictPastTarget:
		return conflictReasonPast
	default:
		return conflictReasonScheduleClose
	}
}

// conflictDTOs 把冲突列表投影为受控视图，保持稳定顺序。
func conflictDTOs(conflicts []domain.OccurrenceConflict) []ConflictDTO {
	result := make([]ConflictDTO, 0, len(conflicts))
	for _, conflict := range conflicts {
		result = append(result, ConflictDTO{
			Target:      occurrenceIdentityDTO(conflict.Target),
			Other:       occurrenceIdentityDTO(conflict.Other),
			SameGroup:   conflict.SameGroup,
			OverlapUnix: conflict.OverlapUnix,
		})
	}
	return result
}

// occurrenceIdentityDTO 把冲突参与方投影为不暴露领域对象的身份视图。
// 尚未持久化的目标使用负数占位标识，对外统一表现为 0 以表示“还没有 ID”。
func occurrenceIdentityDTO(value domain.ConflictOccurrence) OccurrenceIdentityDTO {
	scheduleID := int64(value.ScheduleID)
	if scheduleID < 0 {
		scheduleID = 0
	}
	return OccurrenceIdentityDTO{
		ScheduleID:   scheduleID,
		OriginalDate: value.OriginalDate,
		Title:        value.Title,
		StartUnix:    value.StartUnix,
		EndUnix:      value.EndUnix,
	}
}

// pendingScheduleID 是尚未持久化的新建日程在预览内部使用的临时标识。
// 持久化日程 ID 恒为正数，因此负数不会与任何真实身份冲突，也不会出现在对外 DTO 中。
const pendingScheduleID domain.ScheduleID = -1

// pendingSchedule 把新建候选投影为可参与冲突计算的日程定义。
func pendingSchedule(candidate domain.ScheduleCandidate) domain.Schedule {
	return domain.Schedule{
		ID:    pendingScheduleID,
		Title: candidate.Title, Notes: candidate.Notes, Cron: candidate.Cron,
		UntilDateLocal: candidate.UntilDateLocal, MonthEndPolicy: candidate.MonthEndPolicy,
		Open: candidate.Open, FirstStartLocal: candidate.FirstStartLocal,
		OriginalStartLocal: candidate.OriginalStartLocal, DurationMinutes: candidate.DurationMinutes,
		Revision: 1, CreatedAtUTC: candidate.CreatedAtUTC, UpdatedAtUTC: candidate.UpdatedAtUTC,
	}
}

// replaceOverride 用同一身份的更新快照替换列表中的旧值。
func replaceOverride(overrides []domain.OccurrenceOverride, replacement domain.OccurrenceOverride) []domain.OccurrenceOverride {
	result := make([]domain.OccurrenceOverride, 0, len(overrides)+1)
	replaced := false
	for _, override := range overrides {
		if override.ScheduleID == replacement.ScheduleID && override.OriginalDate == replacement.OriginalDate {
			result = append(result, replacement)
			replaced = true
			continue
		}
		result = append(result, override)
	}
	if !replaced {
		result = append(result, replacement)
	}
	return result
}

// removeOverride 从列表中移除指定身份。
func removeOverride(overrides []domain.OccurrenceOverride, originalDate string) []domain.OccurrenceOverride {
	result := make([]domain.OccurrenceOverride, 0, len(overrides))
	for _, override := range overrides {
		if override.OriginalDate == originalDate {
			continue
		}
		result = append(result, override)
	}
	return result
}

// postChangeOverrides 计算单次变更后的例外集合，供预览和提交后冲突计算复用。
func postChangeOverrides(overrides []domain.OccurrenceOverride, changed domain.OccurrenceOverride) []domain.OccurrenceOverride {
	return replaceOverride(overrides, changed)
}
