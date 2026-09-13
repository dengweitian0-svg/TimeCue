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
	s, err := OpenStore(ctx, path, logger)
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
	s, err = OpenStore(ctx, path, logger)
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
		fmt.Sprintf(`{"until_unix":null,"expected_revision":%d}`, stored.Revision),
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
