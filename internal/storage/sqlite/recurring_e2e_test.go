package sqlite

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// recurringE2EClock 为真实存储端到端测试提供显式时区和可推进的时间。
type recurringE2EClock struct{ snapshot domain.TimeSnapshot }

// Snapshot 返回测试指定的当前时间环境。
func (c *recurringE2EClock) Snapshot(context.Context) (domain.TimeSnapshot, error) {
	return c.snapshot, nil
}

// TestRecurringJSONSQLiteEndToEnd 验证重复 JSON 创建、整组编辑、列表和重开后历史发生计算。
func TestRecurringJSONSQLiteEndToEnd(t *testing.T) {
	ctx := context.Background()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 7, 2, 30, 0, 0, loc)
	until := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	clock := &recurringE2EClock{snapshot: domain.TimeSnapshot{Now: start.Add(-time.Hour), Location: loc}}
	path := filepath.Join(t.TempDir(), "e2e.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	service := app.NewService(s, clock)
	request, err := app.DecodeCreateSchedule([]byte(fmt.Sprintf(`{"title":" DST recurring ","start_unix":%d,"end_unix":%d,"cron":"30 2 * * *","until_unix":%d}`, start.Unix(), start.Add(90*time.Minute).Unix(), until.Unix())))
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSchedule(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "DST recurring" || created.UntilDateLocal == nil || *created.UntilDateLocal != "2026-03-09" || created.DurationMinutes != 90 {
		t.Fatalf("created = %+v", created)
	}
	id := domain.ScheduleID(created.ID)
	before, err := s.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	occurrences, err := domain.Expand(before, start, until.AddDate(0, 0, 1), loc)
	if err != nil || len(occurrences) != 3 || occurrences[1].StartLocal.Format(time.RFC3339) != "2026-03-08T03:30:00-04:00" {
		t.Fatalf("DST expansion = %+v %v", occurrences, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatal(err)
	}
	service = app.NewService(s, clock)
	stored, err := s.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, stored, before)
	recomputed, err := domain.Expand(stored, start, until.AddDate(0, 0, 1), loc)
	if err != nil || !reflect.DeepEqual(occurrences, recomputed) {
		t.Fatalf("reopen changed occurrences: %+v %v", recomputed, err)
	}
	clock.snapshot.Now = start.AddDate(1, 0, 0)
	for _, cron := range []string{"0 9 * * 0", "30 2 * * *"} {
		patch, err := app.DecodeUpdateSchedule([]byte(fmt.Sprintf(`{"cron":%q,"expected_revision":%d}`, cron, stored.Revision)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.UpdateSchedule(ctx, id, patch); err != nil {
			t.Fatal(err)
		}
		updated, err := s.GetSchedule(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		want := stored
		want.Cron, want.Revision, want.UpdatedAtUTC = &cron, stored.Revision+1, clock.snapshot.Now.UTC()
		assertSameSchedule(t, updated, want)
		stored = updated
	}
	newStart, newEnd, newUntil := start.AddDate(0, 0, 1), start.AddDate(0, 0, 1).Add(2*time.Hour), until.AddDate(0, 0, 7)
	patch, err := app.DecodeUpdateSchedule([]byte(fmt.Sprintf(`{"title":"edited","start_unix":%d,"end_unix":%d,"until_unix":%d,"expected_revision":%d}`, newStart.Unix(), newEnd.Unix(), newUntil.Unix(), stored.Revision)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateSchedule(ctx, id, patch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "edited" || result.DurationMinutes != 120 || *result.UntilDateLocal != "2026-03-16" {
		t.Fatalf("edited = %+v", result)
	}
	stored, err = s.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.OriginalStartLocal.Equal(before.OriginalStartLocal) || !stored.CreatedAtUTC.Equal(before.CreatedAtUTC) || stored.Open != before.Open {
		t.Fatal("identity changed")
	}
	for _, payload := range []string{
		fmt.Sprintf(`{"cron":"bad","expected_revision":%d}`, stored.Revision),
		`{"cron":"0 10 * * *","expected_revision":1}`,
	} {
		patch, err := app.DecodeUpdateSchedule([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		var changesBefore, changesAfter int64
		if err := s.db.QueryRow("SELECT total_changes()").Scan(&changesBefore); err != nil {
			t.Fatal(err)
		}
		if _, err := service.UpdateSchedule(ctx, id, patch); err == nil {
			t.Fatalf("accepted %s", payload)
		}
		if err := s.db.QueryRow("SELECT total_changes()").Scan(&changesAfter); err != nil {
			t.Fatal(err)
		}
		if changesAfter != changesBefore {
			t.Fatal("rejected request performed SQL write")
		}
		got, err := s.GetSchedule(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		assertSameSchedule(t, got, stored)
	}
	list, err := service.ListSchedules(ctx, app.ListSchedulesRequest{})
	if err != nil || len(list.Items) != 1 || list.Items[0].UntilDateLocal == nil || *list.Items[0].UntilDateLocal != "2026-03-16" {
		t.Fatalf("list = %+v %v", list, err)
	}
	get, err := service.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := app.EncodeSchedule(get)
	if err != nil || !strings.Contains(string(data), `"until_date_local":"2026-03-16"`) {
		t.Fatalf("encoded = %s %v", data, err)
	}
	assertNoInstances(t, s)
}

// TestOptionalCutoffJSONSQLiteLifecycle 验证重复截止字段的省略、保留、清空、更新和重开行为。
func TestOptionalCutoffJSONSQLiteLifecycle(t *testing.T) {
	ctx := context.Background()
	loc := time.UTC
	first := time.Date(2026, 9, 12, 9, 0, 0, 0, loc)
	clock := &recurringE2EClock{snapshot: domain.TimeSnapshot{Now: first.Add(-time.Hour), Location: loc}}
	path := filepath.Join(t.TempDir(), "optional-cutoff.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	service := app.NewService(store, clock)
	optionalRequest, err := app.DecodeCreateSchedule([]byte(fmt.Sprintf(`{"title":"unlimited","start_unix":%d,"end_unix":%d,"cron":"0 9 * * *"}`, first.Unix(), first.Add(time.Hour).Unix())))
	if err != nil {
		t.Fatal(err)
	}
	optional, err := service.CreateSchedule(ctx, optionalRequest)
	if err != nil || optional.UntilDateLocal != nil {
		t.Fatalf("optional create = %+v, %v", optional, err)
	}
	limitedUntil := first.AddDate(0, 0, 7)
	limitedRequest, err := app.DecodeCreateSchedule([]byte(fmt.Sprintf(`{"title":"limited","start_unix":%d,"end_unix":%d,"cron":"0 9 * * *","until_unix":%d}`, first.Unix(), first.Add(time.Hour).Unix(), limitedUntil.Unix())))
	if err != nil {
		t.Fatal(err)
	}
	limited, err := service.CreateSchedule(ctx, limitedRequest)
	if err != nil || limited.UntilDateLocal == nil {
		t.Fatalf("limited create = %+v, %v", limited, err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatal(err)
	}
	service = app.NewService(store, clock)
	reopened, err := store.GetSchedule(ctx, domain.ScheduleID(limited.ID))
	if err != nil || reopened.UntilDateLocal == nil || *reopened.UntilDateLocal != limitedUntil.Format(time.DateOnly) {
		t.Fatalf("reopened limited = %+v, %v", reopened, err)
	}

	missing, err := app.DecodeUpdateSchedule([]byte(fmt.Sprintf(`{"title":"kept","expected_revision":%d}`, reopened.Revision)))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := service.UpdateSchedule(ctx, domain.ScheduleID(limited.ID), missing)
	if err != nil || kept.UntilDateLocal == nil || *kept.UntilDateLocal != limitedUntil.Format(time.DateOnly) {
		t.Fatalf("missing cutoff update = %+v, %v", kept, err)
	}

	clear, err := app.DecodeUpdateSchedule([]byte(fmt.Sprintf(`{"until_unix":null,"expected_revision":%d}`, kept.Revision)))
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := service.UpdateSchedule(ctx, domain.ScheduleID(limited.ID), clear)
	if err != nil || cleared.UntilDateLocal != nil {
		t.Fatalf("cleared cutoff update = %+v, %v", cleared, err)
	}

	newUntil := first.AddDate(0, 0, 14)
	set, err := app.DecodeUpdateSchedule([]byte(fmt.Sprintf(`{"until_unix":%d,"expected_revision":%d}`, newUntil.Unix(), cleared.Revision)))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateSchedule(ctx, domain.ScheduleID(limited.ID), set)
	if err != nil || updated.UntilDateLocal == nil || *updated.UntilDateLocal != newUntil.Format(time.DateOnly) {
		t.Fatalf("set cutoff update = %+v, %v", updated, err)
	}

	optionalDTO, err := service.GetSchedule(ctx, domain.ScheduleID(optional.ID))
	if err != nil {
		t.Fatal(err)
	}
	optionalData, err := app.EncodeSchedule(optionalDTO)
	if err != nil || !strings.Contains(string(optionalData), `"until_date_local":null`) {
		t.Fatalf("optional JSON = %s, %v", optionalData, err)
	}
}

// TestMonthlyJSONSQLiteLifecycle 验证月规则创建、整组编辑、重开和无副作用计算的一致性。
func TestMonthlyJSONSQLiteLifecycle(t *testing.T) {
	ctx := context.Background()
	loc := time.FixedZone("UTC+8", 8*60*60)
	first := time.Date(2027, 1, 31, 9, 0, 0, 0, loc)
	end := first.Add(90 * time.Minute)
	until := time.Date(2028, 3, 31, 12, 0, 0, 0, loc)
	clock := &recurringE2EClock{snapshot: domain.TimeSnapshot{Now: first.Add(-time.Hour), Location: loc}}
	path := filepath.Join(t.TempDir(), "monthly.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	service := app.NewService(store, clock)

	request, err := app.DecodeCreateSchedule([]byte(fmt.Sprintf(`{"title":" monthly ","start_unix":%d,"end_unix":%d,"cron":"0 9 31 * *","until_unix":%d}`, first.Unix(), end.Unix(), until.Unix())))
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSchedule(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.MonthEndPolicy == nil || *created.MonthEndPolicy != domain.MonthEndPolicyClamp || created.Title != "monthly" {
		t.Fatalf("created = %+v", created)
	}

	id := domain.ScheduleID(created.ID)
	stored, err := store.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	original := stored

	newFirst := time.Date(2027, 1, 31, 10, 0, 0, 0, loc)
	newEnd := newFirst.Add(2 * time.Hour)
	editRequest, err := app.DecodeUpdateSchedule([]byte(fmt.Sprintf(`{"title":"edited monthly","start_unix":%d,"end_unix":%d,"cron":"0 10 29 * *","until_unix":%d,"expected_revision":%d}`, newFirst.Unix(), newEnd.Unix(), until.Unix(), stored.Revision)))
	if err != nil {
		t.Fatal(err)
	}
	edited, err := service.UpdateSchedule(ctx, id, editRequest)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Title != "edited monthly" || edited.DurationMinutes != 120 || edited.MonthEndPolicy == nil || *edited.MonthEndPolicy != domain.MonthEndPolicyClamp || edited.Revision != stored.Revision+1 {
		t.Fatalf("edited = %+v", edited)
	}

	restoreCron := "0 10 31 * *"
	restoreRequest, err := app.DecodeUpdateSchedule([]byte(fmt.Sprintf(`{"cron":%q,"expected_revision":%d}`, restoreCron, edited.Revision)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateSchedule(ctx, id, restoreRequest); err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Cron == nil || *stored.Cron != restoreCron || stored.MonthEndPolicy == nil || *stored.MonthEndPolicy != domain.MonthEndPolicyClamp {
		t.Fatalf("restored = %+v", stored)
	}
	if !stored.OriginalStartLocal.Equal(original.OriginalStartLocal) || !stored.CreatedAtUTC.Equal(original.CreatedAtUTC) || stored.Open != original.Open {
		t.Fatal("edit changed schedule identity")
	}

	got, ok, err := domain.NextBase(stored, newFirst.Add(-time.Minute), loc)
	if err != nil || !ok || !got.StartLocal.Equal(newFirst) {
		t.Fatalf("NextBase before reopen = %+v, %v, %v", got, ok, err)
	}
	baseBefore, baseOK, err := domain.NextBase(stored, newFirst, loc)
	if err != nil || !baseOK || !baseBefore.StartLocal.Equal(time.Date(2027, 2, 28, 10, 0, 0, 0, loc)) {
		t.Fatalf("clamped NextBase = %+v, %v, %v", baseBefore, baseOK, err)
	}
	startBefore, startOK, err := domain.NextStart(stored, newFirst, loc)
	if err != nil || !startOK || !startBefore.Equal(baseBefore.StartLocal) {
		t.Fatalf("NextStart before reopen = %v, %v, %v", startBefore, startOK, err)
	}
	expandBefore, err := domain.Expand(stored, time.Date(2027, 1, 1, 0, 0, 0, 0, loc), time.Date(2027, 4, 1, 0, 0, 0, 0, loc), loc)
	if err != nil || len(expandBefore) != 3 || expandBefore[1].StartLocal.Day() != 28 || expandBefore[2].StartLocal.Day() != 31 {
		t.Fatalf("Expand before reopen = %+v, %v", expandBefore, err)
	}

	get, err := service.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.ListSchedules(ctx, app.ListSchedulesRequest{})
	if err != nil || len(list.Items) != 1 || list.Items[0].MonthEndPolicy == nil || *list.Items[0].MonthEndPolicy != domain.MonthEndPolicyClamp {
		t.Fatalf("list = %+v, %v", list, err)
	}
	getJSON, err := app.EncodeSchedule(get)
	if err != nil || !strings.Contains(string(getJSON), `"month_end_policy":"clamp"`) {
		t.Fatalf("get JSON = %s, %v", getJSON, err)
	}
	listJSON, err := app.EncodeListSchedules(list)
	if err != nil || !strings.Contains(string(listJSON), `"month_end_policy":"clamp"`) {
		t.Fatalf("list JSON = %s, %v", listJSON, err)
	}

	stateBeforeFailure := stored
	for _, payload := range []string{
		fmt.Sprintf(`{"cron":"bad","expected_revision":%d}`, stored.Revision),
		fmt.Sprintf(`{"cron":"0 10 * * 1","expected_revision":1}`),
		fmt.Sprintf(`{"month_end_policy":"clamp","expected_revision":%d}`, stored.Revision),
	} {
		patch, err := app.DecodeUpdateSchedule([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		var changesBefore, changesAfter int64
		if err := store.db.QueryRow("SELECT total_changes()").Scan(&changesBefore); err != nil {
			t.Fatal(err)
		}
		if _, err := service.UpdateSchedule(ctx, id, patch); err == nil {
			t.Fatalf("accepted %s", payload)
		}
		if err := store.db.QueryRow("SELECT total_changes()").Scan(&changesAfter); err != nil {
			t.Fatal(err)
		}
		if changesAfter != changesBefore {
			t.Fatalf("rejected request wrote database: %s", payload)
		}
		current, err := store.GetSchedule(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		assertSameSchedule(t, current, stateBeforeFailure)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatal(err)
	}
	service = app.NewService(store, clock)
	reopened, err := store.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, reopened, stateBeforeFailure)
	baseAfter, baseAfterOK, err := domain.NextBase(reopened, newFirst, loc)
	if err != nil || baseAfterOK != baseOK || !baseAfter.StartLocal.Equal(baseBefore.StartLocal) || !baseAfter.EndLocal.Equal(baseBefore.EndLocal) {
		t.Fatalf("NextBase after reopen = %+v, %v, %v", baseAfter, baseAfterOK, err)
	}
	startAfter, startAfterOK, err := domain.NextStart(reopened, newFirst, loc)
	if err != nil || startAfterOK != startOK || !startAfter.Equal(startBefore) {
		t.Fatalf("NextStart after reopen = %v, %v, %v", startAfter, startAfterOK, err)
	}
	expandAfter, err := domain.Expand(reopened, time.Date(2027, 1, 1, 0, 0, 0, 0, loc), time.Date(2027, 4, 1, 0, 0, 0, 0, loc), loc)
	if err != nil || !reflect.DeepEqual(expandAfter, expandBefore) {
		t.Fatalf("Expand after reopen = %+v, %v; before = %+v", expandAfter, err, expandBefore)
	}
	assertNoInstances(t, store)
}
