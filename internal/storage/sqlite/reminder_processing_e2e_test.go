package sqlite

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// sqliteReminderTestClock 为应用用例提供不读取系统时间的可控快照。
type sqliteReminderTestClock struct {
	snapshot domain.TimeSnapshot
}

// Snapshot 返回预置的时间环境。
func (c *sqliteReminderTestClock) Snapshot(context.Context) (domain.TimeSnapshot, error) {
	return c.snapshot, nil
}

// TestReminderProcessingServiceRebuildsAfterEditAndSurvivesRestartSemantics 验证真实 SQLite 下的详情重建、确认和删除缺失语义。
func TestReminderProcessingServiceRebuildsAfterEditAndSurvivesRestartSemantics(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	schedule, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	identity := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-02"}
	attemptedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)
	insertReminderAttemptTestRow(t, store, identity.ScheduleID, identity.OriginalDate, app.ReminderAttemptSubmitted, attemptedAt, nil)
	clock := &sqliteReminderTestClock{snapshot: domain.TimeSnapshot{Now: attemptedAt.Add(time.Hour), Location: time.FixedZone("UTC+8", 8*60*60)}}
	service := app.NewService(store, clock)

	before, err := service.GetReminderDetail(ctx, identity)
	if err != nil || before.Status != app.ReminderStatusPending || before.ContentStatus != app.ReminderContentAvailable || before.Title == nil || *before.Title != "test schedule" {
		t.Fatalf("before detail = %+v, %v", before, err)
	}
	var processed sql.NullString
	if err := store.db.QueryRow("SELECT processed_at_utc FROM reminder_attempts WHERE schedule_id = ? AND occurrence_local = ?", identity.ScheduleID, identity.OriginalDate).Scan(&processed); err != nil {
		t.Fatalf("read untouched processing state error = %v", err)
	}
	if processed.Valid {
		t.Fatal("reading detail changed processed_at_utc")
	}

	title := "edited title"
	notes := "edited notes"
	updated, err := store.UpdateSchedule(ctx, schedule.ID, domain.ScheduleUpdateCandidate{
		Title: &title, Notes: &notes, ExpectedRevision: schedule.Revision, UpdatedAtUTC: attemptedAt.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("UpdateSchedule() error = %v", err)
	}
	edited, err := service.GetReminderDetail(ctx, identity)
	if err != nil || edited.Title == nil || *edited.Title != title || edited.Notes == nil || *edited.Notes != notes || updated.Revision != schedule.Revision+1 {
		t.Fatalf("edited detail = %+v, updated=%+v, error=%v", edited, updated, err)
	}

	confirmed, err := service.ConfirmReminder(ctx, identity)
	if err != nil || !confirmed.Changed || !confirmed.ProcessedAtUTC.Equal(clock.snapshot.Now.UTC()) {
		t.Fatalf("confirmed = %+v, %v", confirmed, err)
	}
	confirmedDetail, err := service.GetReminderDetail(ctx, identity)
	if err != nil || confirmedDetail.Status != app.ReminderStatusConfirmed || confirmedDetail.Title != nil || confirmedDetail.ContentStatus != app.ReminderContentUnavailable {
		t.Fatalf("confirmed detail = %+v, %v", confirmedDetail, err)
	}

	if _, err := store.DeleteSchedule(ctx, schedule.ID, updated.Revision); err != nil {
		t.Fatalf("DeleteSchedule() error = %v", err)
	}
	missing, err := service.GetReminderDetail(ctx, identity)
	if err != nil || missing.Status != app.ReminderStatusMissing || missing.Title != nil {
		t.Fatalf("deleted detail = %+v, %v", missing, err)
	}
}

// TestReminderProcessingPersistsAcrossStoreReopen 验证重启后确认状态和待处理列表保持一致。
func TestReminderProcessingPersistsAcrossStoreReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "timecue.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	schedule, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		_ = store.Close()
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	identity := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-02"}
	attemptedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)
	insertReminderAttemptTestRow(t, store, identity.ScheduleID, identity.OriginalDate, app.ReminderAttemptSubmitted, attemptedAt, nil)
	if err := store.Close(); err != nil {
		t.Fatalf("close first store error = %v", err)
	}

	reopened, err := OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatalf("reopen store error = %v", err)
	}
	clock := &sqliteReminderTestClock{snapshot: domain.TimeSnapshot{Now: attemptedAt.Add(time.Hour), Location: time.FixedZone("UTC+8", 8*60*60)}}
	service := app.NewService(reopened, clock)
	pending, err := service.ListPendingReminders(ctx, app.ListPendingRemindersRequest{Limit: 10})
	if err != nil || len(pending.Items) != 1 || pending.Items[0].Status != app.ReminderStatusPending {
		t.Fatalf("reopened pending = %+v, %v", pending, err)
	}
	if _, err := service.ConfirmReminder(ctx, identity); err != nil {
		_ = reopened.Close()
		t.Fatalf("reopened confirmation error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close confirmed store error = %v", err)
	}

	confirmedStore, err := OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatalf("reopen confirmed store error = %v", err)
	}
	defer func() { _ = confirmedStore.Close() }()
	confirmedService := app.NewService(confirmedStore, clock)
	confirmed, err := confirmedService.GetReminderDetail(ctx, identity)
	if err != nil || confirmed.Status != app.ReminderStatusConfirmed || confirmed.Title != nil {
		t.Fatalf("reopened confirmed detail = %+v, %v", confirmed, err)
	}
	remaining, err := confirmedService.ListPendingReminders(ctx, app.ListPendingRemindersRequest{Limit: 10})
	if err != nil || len(remaining.Items) != 0 {
		t.Fatalf("reopened pending after confirmation = %+v, %v", remaining, err)
	}
}
