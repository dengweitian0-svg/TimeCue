package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// insertReminderAttemptTestRow 写入测试所需的发送尝试状态。
func insertReminderAttemptTestRow(t *testing.T, store *Store, scheduleID domain.ScheduleID, originalDate string, status app.ReminderAttemptStatus, attemptedAt time.Time, processedAt *time.Time) {
	t.Helper()
	if _, err := store.db.Exec(`
		INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status, processed_at_utc)
		VALUES (?, ?, ?, ?, ?)`, int64(scheduleID), originalDate, formatUTCTime(attemptedAt), string(status), nullableUTCTime(processedAt)); err != nil {
		t.Fatalf("insert reminder attempt %d/%s error = %v", scheduleID, originalDate, err)
	}
}

// nullableUTCTime 将可选确认时间转换为 SQLite NULL 或 UTC 文本。
func nullableUTCTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatUTCTime(*value)
}

// TestListPendingReminderAttemptsStrictlyFiltersProcessedAndNonSubmitted 验证存储分页只返回可处理记录。
func TestListPendingReminderAttemptsStrictlyFiltersProcessedAndNonSubmitted(t *testing.T) {
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	attemptedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)
	insertReminderAttemptTestRow(t, store, schedule.ID, "2026-01-02", app.ReminderAttemptSubmitted, attemptedAt, nil)
	insertReminderAttemptTestRow(t, store, schedule.ID, "2026-01-03", app.ReminderAttemptClaimed, attemptedAt, nil)
	insertReminderAttemptTestRow(t, store, schedule.ID, "2026-01-04", app.ReminderAttemptFailed, attemptedAt, nil)
	insertReminderAttemptTestRow(t, store, schedule.ID, "2026-01-05", app.ReminderAttemptSkipped, attemptedAt, nil)
	insertReminderAttemptTestRow(t, store, schedule.ID, "2026-01-06", app.ReminderAttemptSubmitted, attemptedAt, timePtrForSQLite(attemptedAt.Add(time.Minute)))
	insertReminderAttemptTestRow(t, store, schedule.ID, "2026-01-07", app.ReminderAttemptSubmitted, attemptedAt.Add(time.Minute), nil)

	first, err := store.ListPendingReminderAttempts(context.Background(), app.ListPendingReminderAttemptsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("first pending page error = %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].Identity.OriginalDate != "2026-01-02" || first.NextCursor == 0 {
		t.Fatalf("first pending page = %+v", first)
	}
	second, err := store.ListPendingReminderAttempts(context.Background(), app.ListPendingReminderAttemptsRequest{AfterID: first.NextCursor, Limit: 10})
	if err != nil {
		t.Fatalf("second pending page error = %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Identity.OriginalDate != "2026-01-07" || second.NextCursor != 0 {
		t.Fatalf("second pending page = %+v", second)
	}
}

// TestConfirmReminderAttemptIsAtomicAndIdempotent 验证首次确认、重复确认、缺失和非可处理状态语义。
func TestConfirmReminderAttemptIsAtomicAndIdempotent(t *testing.T) {
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	ctx := context.Background()
	attemptedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)
	pending := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-02"}
	claimed := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-03"}
	insertReminderAttemptTestRow(t, store, pending.ScheduleID, pending.OriginalDate, app.ReminderAttemptSubmitted, attemptedAt, nil)
	insertReminderAttemptTestRow(t, store, claimed.ScheduleID, claimed.OriginalDate, app.ReminderAttemptClaimed, attemptedAt, nil)
	firstAt := attemptedAt.Add(time.Hour)
	first, err := store.ConfirmReminderAttempt(ctx, pending, firstAt)
	if err != nil || !first.Changed || !first.ProcessedAtUTC.Equal(firstAt) {
		t.Fatalf("first confirmation = %+v, %v", first, err)
	}
	repeated, err := store.ConfirmReminderAttempt(ctx, pending, firstAt.Add(time.Hour))
	if err != nil || repeated.Changed || !repeated.ProcessedAtUTC.Equal(firstAt) {
		t.Fatalf("repeated confirmation = %+v, %v", repeated, err)
	}
	var status string
	var processed string
	if err := store.db.QueryRow("SELECT status, processed_at_utc FROM reminder_attempts WHERE schedule_id = ? AND occurrence_local = ?", pending.ScheduleID, pending.OriginalDate).Scan(&status, &processed); err != nil {
		t.Fatalf("read confirmed row error = %v", err)
	}
	if status != string(app.ReminderAttemptSubmitted) || processed != formatUTCTime(firstAt) {
		t.Fatalf("confirmed row = %q/%q", status, processed)
	}
	_, err = store.ConfirmReminderAttempt(ctx, claimed, firstAt)
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.ReminderNotProcessable {
		t.Fatalf("claimed confirmation error = %#v", err)
	}
	_, err = store.ConfirmReminderAttempt(ctx, domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "missing"}, firstAt)
	if !errors.As(err, &businessErr) || businessErr.Code != domain.NotFound {
		t.Fatalf("missing confirmation error = %#v", err)
	}
}

// TestConfirmReminderAttemptConcurrentWritesOnce 验证并发确认只有一次实际状态变化。
func TestConfirmReminderAttemptConcurrentWritesOnce(t *testing.T) {
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	identity := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-02"}
	attemptedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)
	insertReminderAttemptTestRow(t, store, identity.ScheduleID, identity.OriginalDate, app.ReminderAttemptSubmitted, attemptedAt, nil)

	results := make(chan app.ReminderConfirmationResult, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		attempt := i
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, confirmErr := store.ConfirmReminderAttempt(context.Background(), identity, attemptedAt.Add(time.Duration(attempt)*time.Minute))
			results <- result
			errs <- confirmErr
		}()
	}
	wait.Wait()
	close(results)
	close(errs)
	changed := 0
	var firstTime time.Time
	for result := range results {
		if result.Changed {
			changed++
		}
		if firstTime.IsZero() {
			firstTime = result.ProcessedAtUTC
		} else if !firstTime.Equal(result.ProcessedAtUTC) {
			t.Fatalf("concurrent processed times differ: %v and %v", firstTime, result.ProcessedAtUTC)
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent confirmation error = %v", err)
		}
	}
	if changed != 1 {
		t.Fatalf("changed confirmations = %d, want 1", changed)
	}
}

// TestLateReminderStatusWritePreservesProcessedAt 验证迟到的发送状态回写不会覆盖处理时间。
func TestLateReminderStatusWritePreservesProcessedAt(t *testing.T) {
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	identity := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-02"}
	processedAt := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	insertReminderAttemptTestRow(t, store, identity.ScheduleID, identity.OriginalDate, app.ReminderAttemptClaimed, processedAt.Add(-time.Hour), &processedAt)
	if err := store.SetReminderAttemptStatus(context.Background(), identity, app.ReminderAttemptSubmitted); err != nil {
		t.Fatalf("late status update error = %v", err)
	}
	var stored string
	if err := store.db.QueryRow("SELECT processed_at_utc FROM reminder_attempts WHERE schedule_id = ? AND occurrence_local = ?", identity.ScheduleID, identity.OriginalDate).Scan(&stored); err != nil {
		t.Fatalf("read processed time error = %v", err)
	}
	if stored != formatUTCTime(processedAt) {
		t.Fatalf("processed_at_utc = %q, want %q", stored, formatUTCTime(processedAt))
	}
}

// timePtrForSQLite 返回 SQLite 存储测试所需的时间指针。
func timePtrForSQLite(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
