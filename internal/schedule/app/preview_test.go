package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// previewStore 在内存中提供查询源，并记录是否被写入端口调用。
type previewStore struct {
	fakeStore
	sources         []OccurrenceSourceDTO
	reads           map[int64]domain.Schedule
	generation      uint64
	sourceCalls     int
	generationCalls int
	lastSourceReq   ListOccurrenceSourcesRequest
}

// QueryGeneration 返回预置的查询快照版本。
func (s *previewStore) QueryGeneration(context.Context) (uint64, error) {
	s.generationCalls++
	return s.generation, nil
}

// ListOccurrenceSources 按日程 ID 游标返回预置的查询源。
func (s *previewStore) ListOccurrenceSources(_ context.Context, request ListOccurrenceSourcesRequest) (ListOccurrenceSourcesResponse, error) {
	s.sourceCalls++
	s.lastSourceReq = request
	result := ListOccurrenceSourcesResponse{}
	for _, source := range s.sources {
		if source.Schedule.ID > request.AfterScheduleID {
			result.Items = append(result.Items, source)
		}
	}
	return result, nil
}

// GetSchedule 返回与查询源一致的日程定义，缺失时报告未找到。
func (s *previewStore) GetSchedule(_ context.Context, id domain.ScheduleID) (domain.Schedule, error) {
	if schedule, ok := s.reads[int64(id)]; ok {
		return schedule, nil
	}
	return domain.Schedule{}, domain.NewNotFound("schedule was not found")
}

// GetOccurrenceOverride 返回查询源中指定身份的例外，缺失时报告未找到。
func (s *previewStore) GetOccurrenceOverride(_ context.Context, id domain.ScheduleID, originalDate string) (domain.OccurrenceOverride, error) {
	for _, source := range s.sources {
		if source.Schedule.ID != id {
			continue
		}
		for _, override := range source.Overrides {
			if override.OriginalDate == originalDate {
				return override, nil
			}
		}
	}
	return domain.OccurrenceOverride{}, domain.NewNotFound("occurrence override was not found")
}

// written 汇总所有写端口的调用次数，用于验证预览不产生任何副作用。
func (s *previewStore) written() int {
	return s.createCalls + s.updateCalls + s.occurrenceCalls + s.cancelCalls + s.restoreCalls +
		s.scheduleCancelCalls + s.scheduleRestoreCalls + s.deleteCalls
}

// syncSources 让预读端口返回与查询源一致的日程定义，保证预览读取同一份事实。
func (s *previewStore) syncSources() {
	s.reads = make(map[int64]domain.Schedule, len(s.sources))
	for _, source := range s.sources {
		s.reads[int64(source.Schedule.ID)] = source.Schedule
	}
}

// previewScheduleDTO 构造开放重复日程的查询源，首日与 cron 时刻对齐。
func previewScheduleDTO(id int64, title, cron string, until string, firstStart time.Time, durationMinutes int, revision int64) OccurrenceSourceDTO {
	return OccurrenceSourceDTO{Schedule: domain.Schedule{
		ID: domain.ScheduleID(id), Title: title, Cron: &cron, UntilDateLocal: &until, Open: true,
		FirstStartLocal: firstStart, DurationMinutes: durationMinutes, Revision: revision,
	}}
}

// previewSingleDTO 构造开放单次日程的查询源。
func previewSingleDTO(id int64, title string, start time.Time, durationMinutes int, revision int64) OccurrenceSourceDTO {
	return OccurrenceSourceDTO{Schedule: domain.Schedule{
		ID: domain.ScheduleID(id), Title: title, Open: true,
		FirstStartLocal: start, DurationMinutes: durationMinutes, Revision: revision,
	}}
}

// openOverrideDTO 构造开放例外快照，表示该原始日期已经被移动到实际时间。
func openOverrideDTO(id int64, originalDate string, start time.Time, durationMinutes int) domain.OccurrenceOverride {
	return domain.OccurrenceOverride{
		ScheduleID: domain.ScheduleID(id), OriginalDate: originalDate, StartLocal: start,
		DurationMinutes: durationMinutes, Title: "moved", Open: true, SnapshotPresent: true,
	}
}

// cancelledOverrideDTO 构造完整快照的取消例外。
func cancelledOverrideDTO(id int64, originalDate string, start time.Time, durationMinutes int) domain.OccurrenceOverride {
	return domain.OccurrenceOverride{
		ScheduleID: domain.ScheduleID(id), OriginalDate: originalDate, StartLocal: start,
		DurationMinutes: durationMinutes, Title: "cancelled", Open: false, SnapshotPresent: true,
	}
}

// emptyCancelledOverrideDTO 构造没有快照的取消例外。
func emptyCancelledOverrideDTO(id int64, originalDate string) domain.OccurrenceOverride {
	return domain.OccurrenceOverride{ScheduleID: domain.ScheduleID(id), OriginalDate: originalDate, Open: false}
}

// newPreviewStore 创建带复用的日程定义和 9 点冲突日程的预览存储。
func newPreviewStore(location *time.Location) *previewStore {
	store := &previewStore{generation: 11}
	store.sources = append(store.sources,
		previewScheduleDTO(1, "daily", "0 9 * * *", "2026-01-05", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 60, 3),
		previewScheduleDTO(2, "weekly", "0 14 * * 1", "2026-01-05", time.Date(2026, 1, 5, 14, 0, 0, 0, location), 30, 1),
	)
	store.syncSources()
	return store
}

// previewRequest 解码预览 JSON，保证测试覆盖真实解码边界。
func previewRequest(t *testing.T, data string) PreviewChangeRequest {
	t.Helper()
	request, err := DecodePreviewChange([]byte(data))
	if err != nil {
		t.Fatalf("DecodePreviewChange(%s) error = %v", data, err)
	}
	return request
}

// TestPreviewChangeCreateReportsFinalConflict 验证新建预览返回明确状态和冲突身份。
func TestPreviewChangeCreateReportsFinalConflict(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newPreviewStore(location)
	store.syncSources()
	clock := &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}}
	service := NewService(store, clock)

	result, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"create","create":{"title":"new","notes":"","start_unix":`+
			durationUnix(time.Date(2026, 1, 1, 9, 5, 0, 0, location))+
			`,"end_unix":`+durationUnix(time.Date(2026, 1, 1, 9, 35, 0, 0, location))+`}}`))
	if err != nil {
		t.Fatalf("PreviewChange() error = %v", err)
	}
	if result.Operation != ChangeOperationCreate || !result.Checked || !result.HasMore {
		t.Fatalf("preview = %+v", result)
	}
	if result.Conflict.Status != conflictStatusChecked || result.Conflict.Target == nil {
		t.Fatalf("conflict = %+v", result.Conflict)
	}
	if result.Conflict.Target.ScheduleID != 0 || result.Conflict.Target.Title != "new" {
		t.Fatalf("target = %+v", result.Conflict.Target)
	}
	if result.Conflict.BatchUnix != time.Date(2026, 1, 1, 9, 5, 0, 0, location).Unix() {
		t.Fatalf("batch = %d", result.Conflict.BatchUnix)
	}
	if len(result.Conflict.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", result.Conflict.Conflicts)
	}
	conflict := result.Conflict.Conflicts[0]
	if conflict.Other.ScheduleID != 1 || conflict.Other.Title != "daily" || conflict.SameGroup {
		t.Fatalf("conflict = %+v", conflict)
	}
	if conflict.Other.StartUnix != time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix() {
		t.Fatalf("conflict other = %+v", conflict.Other)
	}
	// 预览必须只读：不得调用任何写端口，也不得额外读取时钟。
	if store.written() != 0 {
		t.Fatalf("preview wrote through ports: %+v", store)
	}
	if clock.calls != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.calls)
	}
}

// TestPreviewChangeUpdateMovesIntoConflict 验证整组编辑预览使用变更后的定义计算冲突。
func TestPreviewChangeUpdateMovesIntoConflict(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	// 该日程原本在 09:30 发生，本次编辑把它移动到 10:30。
	start := time.Date(2026, 1, 1, 9, 30, 0, 0, location)
	store := newPreviewStore(location)
	store.sources = append(store.sources, previewScheduleDTO(3, "moved", "30 9 * * *", "2026-01-05", start, 60, 2))
	store.syncSources()
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	result, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"update_schedule","schedule_id":3,"expected_revision":2,"schedule_update":{"start_unix":`+
			durationUnix(time.Date(2026, 1, 1, 10, 30, 0, 0, location))+
			`,"end_unix":`+durationUnix(time.Date(2026, 1, 1, 11, 30, 0, 0, location))+`}}`))
	if err != nil {
		t.Fatalf("PreviewChange() error = %v", err)
	}
	if !result.Checked {
		t.Fatalf("preview = %+v", result)
	}
	// 首次端点早于规则时刻，因此最早的新候选是次日 09:30。
	if result.Conflict.BatchUnix != time.Date(2026, 1, 2, 9, 30, 0, 0, location).Unix() ||
		result.Conflict.Target == nil || result.Conflict.Target.ScheduleID != 3 {
		t.Fatalf("moved target = %+v", result.Conflict)
	}
	// 编辑后的发生与既有 9 点到 10 点的实际区间重叠。
	found := false
	for _, conflict := range result.Conflict.Conflicts {
		if conflict.Other.ScheduleID == 3 && conflict.Other.StartUnix == time.Date(2026, 1, 2, 9, 30, 0, 0, location).Unix() &&
			conflict.OverlapUnix == time.Date(2026, 1, 2, 9, 30, 0, 0, location).Unix() {
			found = true
		}
	}
	if !found {
		t.Fatalf("moved edit missing from conflicts: %+v", result.Conflict.Conflicts)
	}
	if store.written() != 0 {
		t.Fatalf("preview wrote through ports: %+v", store)
	}
}

// TestPreviewChangeRecurringToSingleUsesConversionSelection 验证预览复用重复转单次的候选选择且不写入事实。
func TestPreviewChangeRecurringToSingleUsesConversionSelection(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newPreviewStore(location)
	closed := previewScheduleDTO(5, "paused", "0 9 * * *", "2026-01-03", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30, 2)
	closed.Schedule.Open = false
	store.sources = append(store.sources, closed)
	store.syncSources()
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	result, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"update_schedule","schedule_id":5,"expected_revision":2,"schedule_update":{"cron":null}}`))
	if err != nil {
		t.Fatalf("PreviewChange() error = %v", err)
	}
	if result.Operation != ChangeOperationUpdateSchedule || !result.Checked || result.Conflict.Target == nil ||
		result.Conflict.Target.ScheduleID != 5 || result.Conflict.BatchUnix != time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix() {
		t.Fatalf("conversion preview = %+v", result)
	}
	if store.written() != 0 {
		t.Fatalf("preview wrote through ports: %+v", store)
	}
}

// TestPreviewChangeRestoreScheduleReopensGroup 验证整组恢复预览按重新开放的定义计算。
func TestPreviewChangeRestoreScheduleReopensGroup(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newPreviewStore(location)
	store.syncSources()
	closed := previewScheduleDTO(4, "closed", "0 9 * * *", "2026-01-05", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30, 5)
	closed.Schedule.Open = false
	store.sources = append(store.sources, closed)
	store.syncSources()
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	result, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"restore_schedule","schedule_id":4,"expected_revision":5}`))
	if err != nil {
		t.Fatalf("PreviewChange() error = %v", err)
	}
	if !result.Checked || result.Conflict.Target == nil || result.Conflict.Target.ScheduleID != 4 {
		t.Fatalf("preview = %+v", result)
	}
	// 与既有日程同刻的恢复会发生真实冲突，不是目标身份自身。
	if len(result.Conflict.Conflicts) != 1 {
		t.Fatalf("restored group conflicts = %+v", result.Conflict.Conflicts)
	}
	conflict := result.Conflict.Conflicts[0]
	if conflict.SameGroup || conflict.OverlapUnix != time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix() {
		t.Fatalf("restored group conflict = %+v", conflict)
	}
	if !sameConflictPair(conflict, 1, 4) {
		t.Fatalf("restored group conflict identities = %+v", conflict)
	}
	if store.written() != 0 {
		t.Fatalf("preview wrote through ports: %+v", store)
	}
}

// TestPreviewChangeOccurrenceUpdateChecksOnlyTarget 验证单次编辑只检查指定发生。
func TestPreviewChangeOccurrenceUpdateChecksOnlyTarget(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newPreviewStore(location)
	store.syncSources()
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	result, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"update_occurrence","schedule_id":1,"original_date":"2026-01-02","expected_revision":3,"occurrence_update":{"start_unix":`+
			durationUnix(time.Date(2026, 1, 2, 14, 0, 0, 0, location))+
			`,"end_unix":`+durationUnix(time.Date(2026, 1, 2, 15, 0, 0, 0, location))+`}}`))
	if err != nil {
		t.Fatalf("PreviewChange() error = %v", err)
	}
	if !result.Checked || result.Conflict.Target == nil || result.Conflict.Target.OriginalDate != "2026-01-02" {
		t.Fatalf("preview = %+v", result)
	}
	// 周日程在本周没有 14 点发生，单次编辑的检查目标不应命中其他发生。
	if len(result.Conflict.Conflicts) != 0 {
		t.Fatalf("single update conflicts = %+v", result.Conflict.Conflicts)
	}
	if result.Conflict.Target.StartUnix != time.Date(2026, 1, 2, 14, 0, 0, 0, location).Unix() {
		t.Fatalf("target = %+v", result.Conflict.Target)
	}
	// 目标身份自身不参与比较：同组 9 点发生不能被当成自身冲突。
	for _, conflict := range result.Conflict.Conflicts {
		if conflict.Other.ScheduleID == 1 {
			t.Fatalf("target identity compared with itself: %+v", conflict)
		}
	}
	if store.written() != 0 {
		t.Fatalf("preview wrote through ports: %+v", store)
	}
}

// TestPreviewChangeOccurrenceRestoreReportsPastAndClosed 验证过去发生和父组关闭返回未检查原因。
func TestPreviewChangeOccurrenceRestoreReportsPastAndClosed(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 2, 8, 0, 0, 0, location)
	store := newPreviewStore(location)
	store.syncSources()
	store.sources[0].Overrides = []domain.OccurrenceOverride{
		emptyCancelledOverrideDTO(1, "2026-01-01"),
		emptyCancelledOverrideDTO(1, "2026-01-03"),
	}
	closed := previewScheduleDTO(4, "closed", "0 9 * * *", "2026-01-05", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30, 6)
	closed.Schedule.Open = false
	closed.Overrides = []domain.OccurrenceOverride{cancelledOverrideDTO(4, "2026-01-03", time.Date(2026, 1, 3, 9, 0, 0, 0, location), 30)}
	store.sources = append(store.sources, closed)
	store.syncSources()
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	past, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"restore_occurrence","schedule_id":1,"original_date":"2026-01-01","expected_revision":3}`))
	if err != nil {
		t.Fatalf("PreviewChange(past) error = %v", err)
	}
	if past.Checked || past.Conflict.Status != conflictStatusPastTarget || past.Conflict.Reason != conflictReasonPast {
		t.Fatalf("past preview = %+v", past)
	}
	if past.Conflict.Target != nil || len(past.Conflict.Conflicts) != 0 {
		t.Fatalf("past preview carries conflicts: %+v", past.Conflict)
	}

	closedResult, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"restore_occurrence","schedule_id":4,"original_date":"2026-01-03","expected_revision":6}`))
	if err != nil {
		t.Fatalf("PreviewChange(closed) error = %v", err)
	}
	if closedResult.Checked || closedResult.Conflict.Status != conflictStatusClosed ||
		closedResult.Conflict.Reason != conflictReasonScheduleClose {
		t.Fatalf("closed preview = %+v", closedResult)
	}

	restored, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"restore_occurrence","schedule_id":1,"original_date":"2026-01-03","expected_revision":3}`))
	if err != nil {
		t.Fatalf("PreviewChange(restore) error = %v", err)
	}
	if !restored.Checked || restored.Conflict.Target == nil || restored.Conflict.Target.OriginalDate != "2026-01-03" {
		t.Fatalf("restore preview = %+v", restored)
	}
	if store.written() != 0 {
		t.Fatalf("preview wrote through ports: %+v", store)
	}
}

// TestPreviewChangeReportsNoFutureOccurrence 验证变更后没有未来候选时返回明确状态。
func TestPreviewChangeReportsNoFutureOccurrence(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 2, 1, 8, 0, 0, 0, location)
	store := newPreviewStore(location)
	// 截止日期已经过去，操作后不再产生任何发生。
	store.sources = []OccurrenceSourceDTO{
		previewScheduleDTO(1, "ended", "0 9 * * *", "2026-01-05", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30, 2),
	}
	store.syncSources()
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	result, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"update_schedule","schedule_id":1,"expected_revision":2,"schedule_update":{"title":"renamed"}}`))
	if err != nil {
		t.Fatalf("PreviewChange() error = %v", err)
	}
	if result.Checked || result.HasMore || result.Conflict.Status != conflictStatusNoTarget ||
		result.Conflict.Reason != conflictReasonNoOccurrence {
		t.Fatalf("preview = %+v", result)
	}
	if result.Conflict.Target != nil || result.Conflict.BatchUnix != 0 {
		t.Fatalf("no-future preview carries target: %+v", result.Conflict)
	}

	// 关闭日程上的单次编辑同样报告未检查，不能伪装成成功检查。
	closed := previewScheduleDTO(2, "closed", "0 9 * * *", "2026-01-05", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30, 1)
	closed.Schedule.Open = false
	closed.Overrides = []domain.OccurrenceOverride{
		openOverrideDTO(2, "2026-01-05", time.Date(2026, 1, 5, 9, 0, 0, 0, location), 30),
	}
	store.sources = append(store.sources, closed)
	store.syncSources()
	closedResult, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"update_occurrence","schedule_id":2,"original_date":"2026-01-05","expected_revision":1,"occurrence_update":{"start_unix":`+
			durationUnix(time.Date(2026, 1, 5, 10, 0, 0, 0, location))+
			`,"end_unix":`+durationUnix(time.Date(2026, 1, 5, 10, 30, 0, 0, location))+`}}`))
	if err != nil {
		t.Fatalf("PreviewChange(closed single) error = %v", err)
	}
	if closedResult.Checked || closedResult.Conflict.Status != conflictStatusClosed {
		t.Fatalf("closed single preview = %+v", closedResult)
	}
	if store.written() != 0 {
		t.Fatalf("preview wrote through ports: %+v", store)
	}
}

// TestPreviewChangeRejectsStaleRevisionAndInvalidRequest 验证版本冲突和非法请求不产生查询或写入。
func TestPreviewChangeRejectsStaleRevisionAndInvalidRequest(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newPreviewStore(location)
	store.syncSources()
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	_, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"update_schedule","schedule_id":1,"expected_revision":2,"schedule_update":{"title":"renamed"}}`))
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleRevision {
		t.Fatalf("stale revision error = %#v", err)
	}
	// 版本冲突在读取查询源之前就被拒绝。
	valid, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"update_schedule","schedule_id":1,"expected_revision":3,"schedule_update":{"title":"renamed"}}`))
	if err != nil || !valid.Checked {
		t.Fatalf("valid preview = %+v, %v", valid, err)
	}

	for _, input := range []string{
		`{}`,
		`{"operation":"remove","schedule_id":1,"expected_revision":1}`,
		`{"operation":"create"}`,
		`{"operation":"create","schedule_id":1,"create":{"title":"x","start_unix":0,"end_unix":60}}`,
		`{"operation":"create","create":{"title":"","start_unix":0,"end_unix":60}}`,
		`{"operation":"update_schedule","schedule_id":1,"expected_revision":3}`,
		`{"operation":"restore_occurrence","schedule_id":1,"expected_revision":3}`,
		`{"operation":"update_schedule","original_date":"2026-01-01","schedule_id":1,"expected_revision":3,"schedule_update":{"title":"x"}}`,
		`{"operation":"create","create":{"title":"x","start_unix":0,"end_unix":60},"schedule_update":{"title":"y"}}`,
		`{"operation":"update_occurrence","schedule_id":1,"original_date":"2026-01-02","expected_revision":3}`,
		`{"operation":"restore_occurrence","schedule_id":1,"original_date":"2026-01-02","expected_revision":3,"occurrence_action":{"original_date":"2026-01-03"}}`,
	} {
		t.Run(strings.ReplaceAll(input, `"`, ""), func(t *testing.T) {
			_, err := service.PreviewChange(context.Background(), previewRequest(t, input))
			var validationErr *domain.Error
			if !errors.As(err, &validationErr) || validationErr.Code != domain.Validation {
				t.Fatalf("PreviewChange(%s) error = %#v, want validation", input, err)
			}
		})
	}
	if store.sourceCalls != 1 || store.written() != 0 {
		t.Fatalf("invalid previews touched ports: sources=%d written=%d", store.sourceCalls, store.written())
	}
}

// conflictPair 判断冲突双方是否分别属于两个日程，忽略内部归一化顺序。
func sameConflictPair(conflict ConflictDTO, left, right int64) bool {
	return (conflict.Target.ScheduleID == left && conflict.Other.ScheduleID == right) ||
		(conflict.Target.ScheduleID == right && conflict.Other.ScheduleID == left)
}

// durationUnix 返回时间点的 Unix 秒字符串，供预览 JSON 复用。
func durationUnix(value time.Time) string {
	return strconv.FormatInt(value.Unix(), 10)
}
