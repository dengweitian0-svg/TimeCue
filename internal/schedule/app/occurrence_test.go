package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	json "encoding/json/v2"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// occurrenceServiceFixture 构造只在周一发生的例外应用测试环境。
func occurrenceServiceFixture() (*fakeStore, *fakeClock, domain.Schedule) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, location)
	cron := "0 10 * * 1"
	until := "2026-09-30"
	schedule := domain.Schedule{
		ID:              7,
		Title:           "base title",
		Notes:           "base notes",
		Cron:            &cron,
		UntilDateLocal:  &until,
		FirstStartLocal: time.Date(2026, 9, 1, 10, 0, 0, 0, location),
		DurationMinutes: 60,
		Open:            true,
		Revision:        3,
	}
	return &fakeStore{getResult: schedule, overrideErr: domain.NewNotFound("missing")}, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}}, schedule
}

// TestServiceUpdateOccurrenceBuildsCompleteSnapshot 验证例外补丁会继承并固化完整发生快照。
func TestServiceUpdateOccurrenceBuildsCompleteSnapshot(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	service := NewService(store, clock)
	start := time.Date(2026, 9, 20, 11, 0, 0, 0, clock.snapshot.Location).Unix()
	store.occurrence = domain.OccurrenceUpdateResult{Override: domain.OccurrenceOverride{
		ScheduleID: schedule.ID, OriginalDate: "2026-09-07", StartLocal: time.Unix(start, 0).In(clock.snapshot.Location),
		DurationMinutes: 60, Title: "changed title", Notes: "changed notes", Open: true, SnapshotPresent: true,
	}, ScheduleRevision: schedule.Revision + 1}
	title := " changed title "
	notes := "changed notes"
	got, err := service.UpdateOccurrence(context.Background(), schedule.ID, UpdateOccurrenceRequest{
		OriginalDate: "2026-09-07", StartUnix: &start, Title: &title, Notes: &notes, ExpectedRevision: schedule.Revision,
	})
	if err != nil {
		t.Fatalf("UpdateOccurrence() error = %v", err)
	}
	if got.OriginalDate != "2026-09-07" || got.StartUnix != start || got.EndUnix != start+3600 || got.Title != "changed title" || got.Notes != notes || got.ScheduleRevision != 4 {
		t.Fatalf("occurrence = %+v", got)
	}
	if store.occurrenceCalls != 1 || store.occurrenceCandidate.ExpectedRevision != schedule.Revision || store.occurrenceCandidate.DurationMinutes != 60 {
		t.Fatalf("store candidate/calls = %+v/%d", store.occurrenceCandidate, store.occurrenceCalls)
	}
}

// TestServiceUpdateOccurrenceAllowsExistingOverrideOutsideRule 验证既有例外脱离当前规则后仍可继续编辑。
func TestServiceUpdateOccurrenceAllowsExistingOverrideOutsideRule(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	cutoff := "2026-09-14"
	schedule.UntilDateLocal = &cutoff
	store.getResult = schedule
	start := time.Date(2026, 9, 20, 11, 0, 0, 0, clock.snapshot.Location)
	store.overrideErr = nil
	store.override = domain.OccurrenceOverride{
		ScheduleID: schedule.ID, OriginalDate: "2026-09-07", StartLocal: start,
		DurationMinutes: 90, Title: "old override", Notes: "old notes", Open: true, SnapshotPresent: true,
	}
	notes := "new notes"
	store.override.Notes = notes
	store.occurrence = domain.OccurrenceUpdateResult{Override: store.override, ScheduleRevision: 4}
	got, err := NewService(store, clock).UpdateOccurrence(context.Background(), schedule.ID, UpdateOccurrenceRequest{
		OriginalDate: "2026-09-07", Notes: &notes, ExpectedRevision: schedule.Revision,
	})
	if err != nil {
		t.Fatalf("UpdateOccurrence() error = %v", err)
	}
	if got.StartUnix != start.Unix() || got.EndUnix != start.Add(90*time.Minute).Unix() || got.Title != "old override" || got.Notes != notes {
		t.Fatalf("occurrence = %+v", got)
	}
	if store.occurrenceCandidate.DurationMinutes != 90 || store.occurrenceCandidate.Title != "old override" {
		t.Fatalf("candidate = %+v", store.occurrenceCandidate)
	}
}

// TestServiceUpdateOccurrenceRejectsDateWithoutBase 验证任意不属于当前规则的日期不会创建例外。
func TestServiceUpdateOccurrenceRejectsDateWithoutBase(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	title := "changed"
	_, err := NewService(store, clock).UpdateOccurrence(context.Background(), schedule.ID, UpdateOccurrenceRequest{
		OriginalDate: "2026-09-08", Title: &title, ExpectedRevision: schedule.Revision,
	})
	assertRecurringCode(t, err, domain.Validation)
	if store.occurrenceCalls != 0 {
		t.Fatalf("occurrence writes = %d, want 0", store.occurrenceCalls)
	}
}

// TestServiceUpdateOccurrenceRejectsInvalidPatchBeforeWrite 验证非法端点和空标题在存储写入前被拒绝。
func TestServiceUpdateOccurrenceRejectsInvalidPatchBeforeWrite(t *testing.T) {
	for _, test := range []struct {
		name string
		make func(domain.Schedule) UpdateOccurrenceRequest
	}{
		{name: "bad precision", make: func(schedule domain.Schedule) UpdateOccurrenceRequest {
			start := time.Date(2026, 9, 7, 10, 0, 1, 0, time.FixedZone("UTC+8", 8*60*60)).Unix()
			return UpdateOccurrenceRequest{OriginalDate: "2026-09-07", StartUnix: &start, ExpectedRevision: schedule.Revision}
		}},
		{name: "empty title", make: func(schedule domain.Schedule) UpdateOccurrenceRequest {
			title := "   "
			return UpdateOccurrenceRequest{OriginalDate: "2026-09-07", Title: &title, ExpectedRevision: schedule.Revision}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, clock, schedule := occurrenceServiceFixture()
			_, err := NewService(store, clock).UpdateOccurrence(context.Background(), schedule.ID, test.make(schedule))
			assertRecurringCode(t, err, domain.Validation)
			if store.occurrenceCalls != 0 {
				t.Fatalf("occurrence writes = %d, want 0", store.occurrenceCalls)
			}
		})
	}
}

// TestDecodeAndEncodeOccurrenceJSON 验证例外请求和实际发生 DTO 的 JSON 字段存在性。
func TestDecodeAndEncodeOccurrenceJSON(t *testing.T) {
	request, err := DecodeUpdateOccurrence([]byte(`{"original_date":"2026-09-07","start_unix":0,"title":"changed","notes":"","expected_revision":3}`))
	if err != nil {
		t.Fatalf("DecodeUpdateOccurrence() error = %v", err)
	}
	if !request.OriginalDatePresent || request.OriginalDate != "2026-09-07" || !request.StartUnixPresent || request.StartUnix == nil || *request.StartUnix != 0 || !request.TitlePresent || !request.NotesPresent || request.Notes == nil || *request.Notes != "" || request.ExpectedRevision != 3 {
		t.Fatalf("request = %+v", request)
	}
	data, err := EncodeOccurrence(OccurrenceDTO{ScheduleID: 7, OriginalDate: "2026-09-07", StartUnix: 10, EndUnix: 70, Title: "changed", Notes: "note", ScheduleRevision: 4})
	if err != nil {
		t.Fatalf("EncodeOccurrence() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, field := range []string{"schedule_id", "original_date", "start_unix", "end_unix", "title", "notes", "schedule_revision"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("encoded occurrence missing %q: %s", field, data)
		}
	}
	for _, input := range []string{`null`, `{"original_date":"2026-09-07","start_unix":1.0}`, `{"original_date":1}`} {
		_, err := DecodeUpdateOccurrence([]byte(input))
		var businessErr *domain.Error
		if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
			t.Errorf("DecodeUpdateOccurrence(%q) error = %#v, want validation", input, err)
		}
	}
	if strings.Contains(string(data), "StartLocal") {
		t.Fatalf("encoded occurrence leaked domain fields: %s", data)
	}
}
