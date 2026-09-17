package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const (
	defaultPendingReminderLimit = 50
	maxPendingReminderLimit     = 200
)

// pendingReminderCursor 绑定分页大小、日程事实 generation、排序键和稳定身份。
// 对外只返回其编码结果，调用方不能构造或修改内部游标字段。
type pendingReminderCursor struct {
	Version      int    `json:"v"`
	Limit        int    `json:"limit"`
	Generation   uint64 `json:"generation"`
	SortUnix     int64  `json:"sort_unix"`
	ScheduleID   int64  `json:"schedule_id"`
	OriginalDate string `json:"original_date"`
}

// pendingReminderItem 保存待处理详情及其最终分页排序键。
type pendingReminderItem struct {
	detail   PendingReminderDTO
	sortUnix int64
}

// ListPendingReminders 读取所有待处理尝试，按当前发生事实倒序分页返回。
// 当前事实无法重建时保留记录并以尝试时间作为排序 fallback，不生成虚假正文。
func (s *Service) ListPendingReminders(ctx context.Context, request ListPendingRemindersRequest) (ListPendingRemindersResponse, error) {
	limit := request.Limit
	if limit == 0 {
		limit = defaultPendingReminderLimit
	}
	if limit <= 0 || limit > maxPendingReminderLimit {
		return ListPendingRemindersResponse{}, domain.NewValidation("pending reminder limit is invalid", "limit")
	}
	processingStore, err := s.reminderProcessingStore()
	if err != nil {
		return ListPendingRemindersResponse{}, err
	}
	queryStore, ok := s.store.(OccurrenceQueryStore)
	if !ok {
		return ListPendingRemindersResponse{}, errors.New("schedule store does not support occurrence queries")
	}
	if s.clock == nil {
		return ListPendingRemindersResponse{}, errors.New("schedule clock is not configured")
	}
	cursor, hasCursor, err := decodePendingReminderCursor(request.Cursor)
	if err != nil {
		return ListPendingRemindersResponse{}, err
	}
	if hasCursor && cursor.Limit != limit {
		return ListPendingRemindersResponse{}, domain.NewValidation("pending reminder cursor parameters do not match request", "cursor", "limit")
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ListPendingRemindersResponse{}, fmt.Errorf("snapshot current time: %w", err)
	}
	generation, err := queryStore.QueryGeneration(ctx)
	if err != nil {
		return ListPendingRemindersResponse{}, fmt.Errorf("read pending reminder generation: %w", err)
	}
	if hasCursor && cursor.Generation != generation {
		return ListPendingRemindersResponse{}, domain.NewStaleCursor("pending reminder cursor is stale", "cursor")
	}
	attempts, err := listAllPendingReminderAttempts(ctx, processingStore)
	if err != nil {
		return ListPendingRemindersResponse{}, err
	}
	sources, err := listAllOccurrenceSources(ctx, queryStore, generation)
	if err != nil {
		return ListPendingRemindersResponse{}, err
	}
	sourceBySchedule := make(map[domain.ScheduleID]OccurrenceSourceDTO, len(sources))
	for _, source := range sources {
		sourceBySchedule[source.Schedule.ID] = source
	}
	items := make([]pendingReminderItem, 0, len(attempts))
	for _, attempt := range attempts {
		source, sourceOK := sourceBySchedule[attempt.Identity.ScheduleID]
		items = append(items, pendingReminderItemFromAttempt(attempt, source, sourceOK, snapshot.Location))
	}
	sort.Slice(items, func(i, j int) bool { return pendingReminderSortLess(items[i], items[j]) })
	if err := ensureOccurrenceGeneration(ctx, queryStore, generation); err != nil {
		return ListPendingRemindersResponse{}, err
	}
	start := 0
	if hasCursor {
		for start < len(items) && !pendingReminderAfterCursor(cursor, items[start]) {
			start++
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	response := ListPendingRemindersResponse{Items: make([]PendingReminderDTO, 0, end-start)}
	for _, item := range items[start:end] {
		response.Items = append(response.Items, item.detail)
	}
	if end < len(items) && len(response.Items) > 0 {
		last := items[end-1]
		response.NextCursor = encodePendingReminderCursor(pendingReminderCursor{
			Version: 1, Limit: limit, Generation: generation, SortUnix: last.sortUnix,
			ScheduleID: last.detail.ScheduleID, OriginalDate: last.detail.OriginalDate,
		})
	}
	return response, nil
}

// GetReminderDetail 读取提醒处理状态，并从当前日程事实重建仍待处理的正文。
// 已确认、缺失或不可处理状态不携带正文，避免把当前事实误当成历史快照。
func (s *Service) GetReminderDetail(ctx context.Context, identity domain.OccurrenceIdentity) (ReminderDetailDTO, error) {
	if err := validateReminderIdentity(identity); err != nil {
		return ReminderDetailDTO{}, err
	}
	processingStore, err := s.reminderProcessingStore()
	if err != nil {
		return ReminderDetailDTO{}, err
	}
	attempt, err := processingStore.GetReminderAttempt(ctx, identity)
	if err != nil {
		if isDomainCode(err, domain.NotFound) {
			return ReminderDetailDTO{ScheduleID: int64(identity.ScheduleID), OriginalDate: identity.OriginalDate,
				ContentStatus: ReminderContentUnavailable, Status: ReminderStatusMissing}, nil
		}
		return ReminderDetailDTO{}, fmt.Errorf("read reminder detail %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
	}
	detail := reminderDetailFromAttempt(attempt)
	if detail.Status != ReminderStatusPending {
		return detail, nil
	}
	if s.clock == nil {
		return ReminderDetailDTO{}, errors.New("schedule clock is not configured")
	}
	sourceReader, ok := s.store.(OccurrenceSourceReader)
	if !ok {
		return ReminderDetailDTO{}, errors.New("schedule store does not support occurrence source reads")
	}
	source, sourceErr := sourceReader.GetOccurrenceSource(ctx, identity.ScheduleID)
	if sourceErr != nil {
		if isDomainCode(sourceErr, domain.NotFound) {
			return detail, nil
		}
		return ReminderDetailDTO{}, fmt.Errorf("read reminder occurrence source %d: %w", identity.ScheduleID, sourceErr)
	}
	snapshot, snapshotErr := s.clock.Snapshot(ctx)
	if snapshotErr != nil {
		return ReminderDetailDTO{}, fmt.Errorf("snapshot current time: %w", snapshotErr)
	}
	occurrence, ok, occurrenceErr := domain.OccurrenceForIdentity(source.Schedule, source.Overrides, identity, snapshot.Location)
	if occurrenceErr != nil || !ok {
		return detail, nil
	}
	fillReminderContent(&detail, occurrence)
	return detail, nil
}

// ConfirmReminder 使用一次时间快照通过协调器进入正式确认写入边界。
// 存储端只有在事务提交成功后才返回成功，重复调用保留首次确认时间。
func (s *Service) ConfirmReminder(ctx context.Context, identity domain.OccurrenceIdentity) (ReminderConfirmationDTO, error) {
	if err := validateReminderIdentity(identity); err != nil {
		return ReminderConfirmationDTO{}, err
	}
	processingStore, err := s.reminderProcessingStore()
	if err != nil {
		return ReminderConfirmationDTO{}, err
	}
	if s.clock == nil {
		return ReminderConfirmationDTO{}, errors.New("schedule clock is not configured")
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ReminderConfirmationDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	var result ReminderConfirmationResult
	if err := s.withMutation(func() error {
		var confirmErr error
		result, confirmErr = processingStore.ConfirmReminderAttempt(ctx, identity, snapshot.Now.UTC())
		return confirmErr
	}); err != nil {
		return ReminderConfirmationDTO{}, fmt.Errorf("confirm reminder %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
	}
	return ReminderConfirmationDTO{ScheduleID: int64(identity.ScheduleID), OriginalDate: identity.OriginalDate,
		ProcessedAtUTC: result.ProcessedAtUTC.UTC(), Changed: result.Changed}, nil
}

// reminderProcessingStore 提取提醒处理用例所需的最小持久化端口。
func (s *Service) reminderProcessingStore() (ReminderProcessingStore, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("schedule store is not configured")
	}
	processingStore, ok := s.store.(ReminderProcessingStore)
	if !ok {
		return nil, errors.New("schedule store does not support reminder processing")
	}
	return processingStore, nil
}

// listAllPendingReminderAttempts 通过存储窄分页读取完整的待处理候选集合。
func listAllPendingReminderAttempts(ctx context.Context, store ReminderProcessingStore) ([]ReminderAttemptRecord, error) {
	const pageLimit = maxPendingReminderLimit
	var (
		afterID int64
		result  []ReminderAttemptRecord
	)
	for {
		page, err := store.ListPendingReminderAttempts(ctx, ListPendingReminderAttemptsRequest{AfterID: afterID, Limit: pageLimit})
		if err != nil {
			return nil, fmt.Errorf("list pending reminder attempts after %d: %w", afterID, err)
		}
		result = append(result, page.Items...)
		if page.NextCursor == 0 {
			return result, nil
		}
		if page.NextCursor <= afterID {
			return nil, errors.New("pending reminder attempt cursor did not advance")
		}
		afterID = page.NextCursor
	}
}

// listAllOccurrenceSources 读取分页日程事实，并在每个批次后检查 generation。
func listAllOccurrenceSources(ctx context.Context, store OccurrenceQueryStore, generation uint64) ([]OccurrenceSourceDTO, error) {
	var (
		afterID domain.ScheduleID
		result  []OccurrenceSourceDTO
	)
	for {
		page, err := store.ListOccurrenceSources(ctx, ListOccurrenceSourcesRequest{AfterScheduleID: afterID, Limit: occurrenceSourceBatchLimit})
		if err != nil {
			return nil, fmt.Errorf("list occurrence sources after schedule %d: %w", afterID, err)
		}
		result = append(result, page.Items...)
		if err := ensureOccurrenceGeneration(ctx, store, generation); err != nil {
			return nil, err
		}
		if page.NextCursor == 0 {
			return result, nil
		}
		if page.NextCursor <= afterID {
			return nil, errors.New("occurrence source cursor did not advance")
		}
		afterID = page.NextCursor
	}
}

// pendingReminderItemFromAttempt 将存储尝试和当前日程事实组合为待处理视图。
func pendingReminderItemFromAttempt(attempt ReminderAttemptRecord, source OccurrenceSourceDTO, sourceOK bool, location *time.Location) pendingReminderItem {
	detail := reminderDetailFromAttempt(attempt)
	sortUnix := attempt.AttemptedAtUTC.Unix()
	if sourceOK {
		occurrence, ok, err := domain.OccurrenceForIdentity(source.Schedule, source.Overrides, attempt.Identity, location)
		if err == nil && ok {
			fillReminderContent(&detail, occurrence)
			sortUnix = occurrence.StartLocal.Unix()
		}
	}
	return pendingReminderItem{detail: detail, sortUnix: sortUnix}
}

// reminderDetailFromAttempt 创建不带正文的提醒详情基础视图。
func reminderDetailFromAttempt(attempt ReminderAttemptRecord) ReminderDetailDTO {
	detail := ReminderDetailDTO{ScheduleID: int64(attempt.Identity.ScheduleID), OriginalDate: attempt.Identity.OriginalDate,
		AttemptedAtUTC: attempt.AttemptedAtUTC.UTC(), ContentStatus: ReminderContentUnavailable}
	if attempt.ProcessedAtUTC != nil {
		processedAt := attempt.ProcessedAtUTC.UTC()
		detail.ProcessedAtUTC = &processedAt
		detail.Status = ReminderStatusConfirmed
	} else if attempt.Status == ReminderAttemptSubmitted {
		detail.Status = ReminderStatusPending
	} else {
		detail.Status = ReminderStatusNotProcessable
	}
	return detail
}

// fillReminderContent 将可重建的发生字段写入受控详情视图。
func fillReminderContent(detail *ReminderDetailDTO, occurrence domain.Occurrence) {
	startUnix := occurrence.StartLocal.Unix()
	endUnix := occurrence.EndLocal.Unix()
	title := occurrence.Title
	notes := occurrence.Notes
	detail.StartUnix = &startUnix
	detail.EndUnix = &endUnix
	detail.Title = &title
	detail.Notes = &notes
	detail.ContentStatus = ReminderContentAvailable
}

// pendingReminderSortLess 比较待处理提醒的倒序发生时间和稳定身份。
func pendingReminderSortLess(left, right pendingReminderItem) bool {
	if left.sortUnix != right.sortUnix {
		return left.sortUnix > right.sortUnix
	}
	if left.detail.ScheduleID != right.detail.ScheduleID {
		return left.detail.ScheduleID < right.detail.ScheduleID
	}
	return left.detail.OriginalDate < right.detail.OriginalDate
}

// pendingReminderAfterCursor 判断条目是否严格位于倒序游标之后。
func pendingReminderAfterCursor(cursor pendingReminderCursor, item pendingReminderItem) bool {
	if item.sortUnix != cursor.SortUnix {
		return item.sortUnix < cursor.SortUnix
	}
	if item.detail.ScheduleID != cursor.ScheduleID {
		return item.detail.ScheduleID > cursor.ScheduleID
	}
	return item.detail.OriginalDate > cursor.OriginalDate
}

// encodePendingReminderCursor 将稳定分页状态编码为不透明游标。
func encodePendingReminderCursor(cursor pendingReminderCursor) string {
	data, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// decodePendingReminderCursor 解码并校验待处理提醒游标。
func decodePendingReminderCursor(value string) (pendingReminderCursor, bool, error) {
	if value == "" {
		return pendingReminderCursor{}, false, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return pendingReminderCursor{}, false, domain.NewValidation("pending reminder cursor is invalid", "cursor")
	}
	var cursor pendingReminderCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.Limit <= 0 || cursor.Limit > maxPendingReminderLimit || cursor.Generation == 0 || cursor.ScheduleID <= 0 || cursor.OriginalDate == "" {
		return pendingReminderCursor{}, false, domain.NewValidation("pending reminder cursor is invalid", "cursor")
	}
	return cursor, true, nil
}

// validateReminderIdentity 校验应用层提醒用例需要的稳定身份。
func validateReminderIdentity(identity domain.OccurrenceIdentity) error {
	if identity.ScheduleID <= 0 || identity.OriginalDate == "" {
		return domain.NewValidation("reminder identity is invalid", "schedule_id", "original_date")
	}
	return nil
}

// isDomainCode 判断错误链中是否包含指定业务分类。
func isDomainCode(err error, code domain.Code) bool {
	var businessErr *domain.Error
	return errors.As(err, &businessErr) && businessErr.Code == code
}
