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

// TestRecoveryClaimReclaimsOnlyClaimedAndCommitsCheckpoint 验证新身份插入、遗留 claimed 重占用和 checkpoint 原子提交。
func TestRecoveryClaimReclaimsOnlyClaimedAndCommitsCheckpoint(t *testing.T) {
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	identity := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-02"}
	firstAttempt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)
	claims, err := store.ClaimRecoveryBatch(context.Background(), []domain.OccurrenceIdentity{identity}, firstAttempt)
	if err != nil || len(claims) != 1 || claims[0].Mode != app.RecoveryCandidateNew {
		t.Fatalf("new recovery claim = %#v, %v", claims, err)
	}
	secondAttempt := firstAttempt.Add(time.Minute)
	claims, err = store.ClaimRecoveryBatch(context.Background(), []domain.OccurrenceIdentity{identity}, secondAttempt)
	if err != nil || len(claims) != 1 || claims[0].Mode != app.RecoveryCandidateReclaim {
		t.Fatalf("claimed recovery reclaim = %#v, %v", claims, err)
	}
	if err := store.CommitRecoveryBatch(context.Background(), app.RecoveryBatchCommit{
		Results:        []app.RecoveryBatchResult{{Identity: identity, Status: app.ReminderAttemptSubmitted}},
		CheckpointUnix: firstAttempt.Unix(), UpdatedAtUTC: secondAttempt,
	}); err != nil {
		t.Fatalf("CommitRecoveryBatch() error = %v", err)
	}
	claims, err = store.ClaimRecoveryBatch(context.Background(), []domain.OccurrenceIdentity{identity}, secondAttempt.Add(time.Minute))
	if err != nil {
		t.Fatalf("submitted recovery claim error = %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("submitted recovery claim = %#v, want no retry", claims)
	}
	checkpoint, err := store.GetSchedulerRecoveryCheckpoint(context.Background())
	if err != nil || !checkpoint.Exists || checkpoint.CheckpointUnix != firstAttempt.Unix() {
		t.Fatalf("checkpoint = %#v, %v", checkpoint, err)
	}
}

// TestCommitRecoveryBatchRollsBackAttemptAndCheckpoint 验证结果回写失败时状态与 checkpoint 同时回滚。
func TestCommitRecoveryBatchRollsBackAttemptAndCheckpoint(t *testing.T) {
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	identity := domain.OccurrenceIdentity{ScheduleID: schedule.ID, OriginalDate: "2026-01-02"}
	attemptedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)
	if _, err := store.ClaimRecoveryBatch(context.Background(), []domain.OccurrenceIdentity{identity}, attemptedAt); err != nil {
		t.Fatalf("ClaimRecoveryBatch() error = %v", err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_recovery_checkpoint
		BEFORE INSERT ON scheduler_recovery_checkpoint
		BEGIN SELECT RAISE(ABORT, 'forced recovery checkpoint failure'); END`); err != nil {
		t.Fatalf("create checkpoint trigger error = %v", err)
	}
	if err := store.CommitRecoveryBatch(context.Background(), app.RecoveryBatchCommit{
		Results:        []app.RecoveryBatchResult{{Identity: identity, Status: app.ReminderAttemptSubmitted}},
		CheckpointUnix: attemptedAt.Unix(), UpdatedAtUTC: attemptedAt,
	}); err == nil {
		t.Fatal("CommitRecoveryBatch() error = nil")
	}
	var status string
	if err := store.db.QueryRow("SELECT status FROM reminder_attempts WHERE schedule_id = ? AND occurrence_local = ?", identity.ScheduleID, identity.OriginalDate).Scan(&status); err != nil {
		t.Fatalf("read rolled back attempt error = %v", err)
	}
	if status != string(app.ReminderAttemptClaimed) {
		t.Fatalf("rolled back status = %q, want claimed", status)
	}
	checkpoint, err := store.GetSchedulerRecoveryCheckpoint(context.Background())
	if err != nil {
		t.Fatalf("read rolled back checkpoint error = %v", err)
	}
	if checkpoint.Exists {
		t.Fatalf("rolled back checkpoint = %#v, want absent", checkpoint)
	}
}
