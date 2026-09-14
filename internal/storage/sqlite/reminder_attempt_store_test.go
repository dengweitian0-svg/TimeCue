package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestReminderAttemptClaimAndOutcomeLifecycle 验证稳定身份只允许一次占用并按有限状态回写。
func TestReminderAttemptClaimAndOutcomeLifecycle(t *testing.T) {
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	identity := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-02"}
	attemptedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)
	claimed, err := store.ClaimReminderAttempt(context.Background(), identity, attemptedAt)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	claimed, err = store.ClaimReminderAttempt(context.Background(), identity, attemptedAt.Add(time.Minute))
	if err != nil || claimed {
		t.Fatalf("duplicate claim = %v, %v", claimed, err)
	}
	if err := store.SetReminderAttemptStatus(context.Background(), identity, app.ReminderAttemptSubmitted); err != nil {
		t.Fatalf("submit result error = %v", err)
	}
	if err := store.SetReminderAttemptStatus(context.Background(), identity, app.ReminderAttemptFailed); err == nil {
		t.Fatal("terminal status rewrite error = nil")
	}
	var status string
	if err := store.db.QueryRow("SELECT status FROM reminder_attempts WHERE schedule_id = ? AND occurrence_local = ?", identity.ScheduleID, identity.OriginalDate).Scan(&status); err != nil {
		t.Fatalf("read attempt status error = %v", err)
	}
	if status != string(app.ReminderAttemptSubmitted) {
		t.Fatalf("status = %q", status)
	}
}

// TestReminderAttemptMigrationRejectsInvalidStatus 验证 SQLite 最后一道状态约束拒绝未知状态。
func TestReminderAttemptMigrationRejectsInvalidStatus(t *testing.T) {
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if _, err := store.db.Exec("INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status) VALUES (?, ?, ?, ?)", schedule.ID, "2026-01-02", "2026-01-02T02:00:00Z", "sent"); err == nil {
		t.Fatal("invalid attempt status error = nil")
	}
}
