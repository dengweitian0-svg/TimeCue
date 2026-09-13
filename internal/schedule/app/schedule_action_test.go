package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestDecodeScheduleActionTracksFieldPresence 验证整组操作请求保留版本字段存在性。
func TestDecodeScheduleActionTracksFieldPresence(t *testing.T) {
	request, err := DecodeScheduleAction([]byte(`{"expected_revision":3}`))
	if err != nil {
		t.Fatalf("DecodeScheduleAction() error = %v", err)
	}
	if request.ExpectedRevision != 3 || !request.ExpectedRevisionPresent {
		t.Fatalf("request = %+v", request)
	}
}

// TestDecodeScheduleActionRejectsInvalidBoundary 验证整组操作 JSON 拒绝 null、数组和非整数版本。
func TestDecodeScheduleActionRejectsInvalidBoundary(t *testing.T) {
	for _, input := range []string{"null", "[]", `{"expected_revision":1.0}`} {
		_, err := DecodeScheduleAction([]byte(input))
		var businessErr *domain.Error
		if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
			t.Errorf("DecodeScheduleAction(%q) error = %#v, want validation", input, err)
		}
	}
}

// TestEncodeScheduleActionUsesControlledFields 验证整组动作 DTO 只输出四个公共字段。
func TestEncodeScheduleActionUsesControlledFields(t *testing.T) {
	data, err := EncodeScheduleAction(ScheduleActionDTO{
		ScheduleID: 1, Open: false, Revision: 4, UpdatedAtUTC: time.Date(2026, 9, 12, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("EncodeScheduleAction() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(decoded) != 4 {
		t.Fatalf("encoded fields = %#v", decoded)
	}
	for _, field := range []string{"schedule_id", "open", "revision", "updated_at_utc"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("encoded result missing %q: %s", field, data)
		}
	}
}

// TestEncodeOccurrenceActionIncludesScheduleOpen 验证例外动作结果回流整组有效状态。
func TestEncodeOccurrenceActionIncludesScheduleOpen(t *testing.T) {
	data, err := EncodeOccurrenceAction(OccurrenceActionDTO{ScheduleID: 1, OriginalDate: "2026-09-07", ScheduleOpen: false})
	if err != nil || !containsJSONField(data, "schedule_open") {
		t.Fatalf("encoded = %s, error = %v", data, err)
	}
}

// containsJSONField 判断 JSON 对象是否包含指定字段。
func containsJSONField(data []byte, field string) bool {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return false
	}
	_, ok := decoded[field]
	return ok
}

// TestServiceCancelSchedulePreReadsAndMapsResult 验证整组取消预读版本、只取一次时钟并返回最小 DTO。
func TestServiceCancelSchedulePreReadsAndMapsResult(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.scheduleCancelResult = domain.ScheduleActionResult{
		Open: false, Revision: schedule.Revision + 1,
		UpdatedAtUTC: time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC), Changed: true,
	}
	got, err := NewService(store, clock).CancelSchedule(context.Background(), schedule.ID, ScheduleActionRequest{ExpectedRevision: schedule.Revision})
	if err != nil {
		t.Fatalf("CancelSchedule() error = %v", err)
	}
	if got.ScheduleID != int64(schedule.ID) || got.Open || got.Revision != schedule.Revision+1 || got.UpdatedAtUTC.Location() != time.UTC {
		t.Fatalf("result = %+v", got)
	}
	if store.scheduleCancelCalls != 1 || store.scheduleCancelCandidate.ExpectedRevision != schedule.Revision || clock.calls != 1 {
		t.Fatalf("calls/candidate/clock = %d/%+v/%d", store.scheduleCancelCalls, store.scheduleCancelCandidate, clock.calls)
	}
}

// TestServiceRestoreScheduleRejectsSingle 验证单次日程不能走整组恢复写端口。
func TestServiceRestoreScheduleRejectsSingle(t *testing.T) {
	location := time.UTC
	schedule := domain.Schedule{ID: 3, FirstStartLocal: time.Date(2026, 9, 1, 10, 0, 0, 0, location), DurationMinutes: 60, Revision: 1, Open: false}
	store := &fakeStore{getResult: schedule}
	clock := &fakeClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 9, 1, 1, 0, 0, 0, location), Location: location}}
	_, err := NewService(store, clock).RestoreSchedule(context.Background(), schedule.ID, ScheduleActionRequest{ExpectedRevision: 1})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
		t.Fatalf("error = %#v, want validation", err)
	}
	if store.scheduleRestoreCalls != 0 || clock.calls != 0 {
		t.Fatalf("restore calls/clock = %d/%d", store.scheduleRestoreCalls, clock.calls)
	}
}

// TestServiceCancelScheduleRejectsStaleWithoutWrite 验证预读发现旧版本时不读取时钟且不调用写端口。
func TestServiceCancelScheduleRejectsStaleWithoutWrite(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	_, err := NewService(store, clock).CancelSchedule(context.Background(), schedule.ID, ScheduleActionRequest{ExpectedRevision: schedule.Revision + 1})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleRevision {
		t.Fatalf("error = %#v, want stale revision", err)
	}
	if store.scheduleCancelCalls != 0 || clock.calls != 0 {
		t.Fatalf("cancel calls/clock = %d/%d", store.scheduleCancelCalls, clock.calls)
	}
}

// TestServiceRestoreOccurrenceProjectsScheduleOpen 验证父组关闭时例外恢复结果仍回流关闭状态。
func TestServiceRestoreOccurrenceProjectsScheduleOpen(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.overrideErr = nil
	store.override = domain.OccurrenceOverride{ScheduleID: schedule.ID, OriginalDate: "2026-09-07", Open: false, SnapshotPresent: true, StartLocal: clock.snapshot.Now, DurationMinutes: 60, Title: "moved"}
	store.restoreResult = domain.OccurrenceActionResult{ScheduleOpen: false, Override: store.override, OverridePresent: true, ScheduleRevision: schedule.Revision + 1, Changed: true}
	store.restoreResult.Override.Open = true
	got, err := NewService(store, clock).RestoreOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{OriginalDate: "2026-09-07", ExpectedRevision: schedule.Revision})
	if err != nil {
		t.Fatalf("RestoreOccurrence() error = %v", err)
	}
	if got.ScheduleOpen || got.Cancelled {
		t.Fatalf("result = %+v", got)
	}
}
