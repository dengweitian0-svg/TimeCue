package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// recoveryTestStore 是只保存测试事实的恢复端口，实现生产端口的最小可替换形状。
type recoveryTestStore struct {
	*schedulerTestSources
	mu         sync.Mutex
	attempts   map[domain.OccurrenceIdentity]ReminderAttemptRecord
	nextID     int64
	checkpoint RecoveryCheckpoint
}

// GetSchedulerRecoveryCheckpoint 返回测试中的单例 checkpoint。
func (s *recoveryTestStore) GetSchedulerRecoveryCheckpoint(context.Context) (RecoveryCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpoint, nil
}

// ListReminderAttemptsForRecovery 按稳定 ID 分页返回测试提醒尝试。
func (s *recoveryTestStore) ListReminderAttemptsForRecovery(_ context.Context, request RecoveryAttemptListRequest) (RecoveryAttemptListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]ReminderAttemptRecord, 0, len(s.attempts))
	for _, item := range s.attempts {
		if item.ID > request.AfterID {
			items = append(items, item)
		}
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].ID < items[i].ID {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	limit := request.Limit
	if limit == 0 {
		limit = 200
	}
	response := RecoveryAttemptListResponse{Items: items}
	if len(response.Items) > limit {
		response.Items = response.Items[:limit]
		response.NextCursor = response.Items[len(response.Items)-1].ID
	}
	return response, nil
}

// ClaimRecoveryBatch 在内存中复现新身份插入和 claimed 重占用。
func (s *recoveryTestStore) ClaimRecoveryBatch(_ context.Context, identities []domain.OccurrenceIdentity, attemptedAt time.Time) ([]RecoveryClaimResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]RecoveryClaimResult, 0, len(identities))
	for _, identity := range identities {
		item, exists := s.attempts[identity]
		if !exists {
			s.nextID++
			s.attempts[identity] = ReminderAttemptRecord{ID: s.nextID, Identity: identity, AttemptedAtUTC: attemptedAt.UTC(), Status: ReminderAttemptClaimed}
			result = append(result, RecoveryClaimResult{Identity: identity, Mode: RecoveryCandidateNew})
			continue
		}
		if item.Status != ReminderAttemptClaimed {
			continue
		}
		item.AttemptedAtUTC = attemptedAt.UTC()
		s.attempts[identity] = item
		result = append(result, RecoveryClaimResult{Identity: identity, Mode: RecoveryCandidateReclaim})
	}
	return result, nil
}

// CommitRecoveryBatch 在内存中复现结果和 checkpoint 的原子提交。
func (s *recoveryTestStore) CommitRecoveryBatch(_ context.Context, commit RecoveryBatchCommit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, result := range commit.Results {
		item, exists := s.attempts[result.Identity]
		if !exists || item.Status != ReminderAttemptClaimed {
			return context.Canceled
		}
		item.Status = result.Status
		s.attempts[result.Identity] = item
	}
	s.checkpoint = RecoveryCheckpoint{Exists: true, CheckpointUnix: commit.CheckpointUnix, UpdatedAtUTC: commit.UpdatedAtUTC}
	return nil
}

// ClaimReminderAttempt 满足普通调度端口，测试恢复路径不依赖其细节。
func (s *recoveryTestStore) ClaimReminderAttempt(ctx context.Context, identity domain.OccurrenceIdentity, attemptedAt time.Time) (bool, error) {
	claims, err := s.ClaimRecoveryBatch(ctx, []domain.OccurrenceIdentity{identity}, attemptedAt)
	return len(claims) == 1, err
}

// SetReminderAttemptStatus 满足普通调度端口并复用测试状态表。
func (s *recoveryTestStore) SetReminderAttemptStatus(_ context.Context, identity domain.OccurrenceIdentity, status ReminderAttemptStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.attempts[identity]
	item.Status = status
	s.attempts[identity] = item
	return nil
}

// TestExpandRecoveryCandidatesSortsAndFiltersStatuses 验证恢复展开的时间排序、稳定身份和终态过滤。
func TestExpandRecoveryCandidatesSortsAndFiltersStatuses(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	cron := "0 9 * * *"
	until := "2026-01-05"
	sources := []OccurrenceSourceDTO{
		{Schedule: domain.Schedule{ID: 2, Open: true, Cron: &cron, UntilDateLocal: &until, FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: 10, Revision: 4}},
		{Schedule: domain.Schedule{ID: 1, Open: true, FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: 10, Revision: 2}},
	}
	claimed := domain.OccurrenceIdentity{ScheduleID: 2, OriginalDate: "2026-01-02"}
	submitted := domain.OccurrenceIdentity{ScheduleID: 2, OriginalDate: "2026-01-03"}
	failed := domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-01"}
	attempts := []ReminderAttemptRecord{
		{ID: 1, Identity: claimed, Status: ReminderAttemptClaimed},
		{ID: 2, Identity: submitted, Status: ReminderAttemptSubmitted},
		{ID: 3, Identity: failed, Status: ReminderAttemptFailed},
	}
	candidates, err := expandRecoveryCandidates(sources, attempts, RecoveryCheckpoint{}, time.Date(2026, 1, 4, 0, 0, 0, 0, location).Unix(), location)
	if err != nil {
		t.Fatalf("expandRecoveryCandidates() error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(candidates))
	}
	for index := 1; index < len(candidates); index++ {
		if candidates[index].Occurrence.StartLocal.Before(candidates[index-1].Occurrence.StartLocal) {
			t.Fatalf("candidates are not chronological: %#v", candidates)
		}
	}
	if candidates[1].Occurrence.Identity() != claimed || candidates[1].Mode != RecoveryCandidateReclaim {
		t.Fatalf("claimed candidate = %#v, want reclaim at stable position", candidates[1])
	}
}

// TestRecoverySchedulerProcessesBatchesAndPublishesCompletion 验证唯一 scheduler goroutine 按批次发送并在 checkpoint 后完成。
func TestRecoverySchedulerProcessesBatchesAndPublishesCompletion(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, location)
	sources := &schedulerTestSources{items: []OccurrenceSourceDTO{{
		Schedule: domain.Schedule{ID: 1, Open: true, FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: 15, Revision: 1},
	}}}
	store := &recoveryTestStore{schedulerTestSources: sources, attempts: make(map[domain.OccurrenceIdentity]ReminderAttemptRecord)}
	clock := &schedulerTestClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}}
	notifier := &schedulerTestNotifier{}
	scheduler := NewReminderScheduler(store, store, notifier, clock, nil, nil, NewMutationCoordinator())
	var events []RecoveryEvent
	scheduler.SetRecoveryEventHandler(func(event RecoveryEvent) { events = append(events, event) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	deadline := time.After(2 * time.Second)
	for {
		notifier.mu.Lock()
		count := len(notifier.items)
		notifier.mu.Unlock()
		if count == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("recovery notification was not submitted: status=%+v", scheduler.GetRuntimeStatus())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	status := scheduler.GetRuntimeStatus()
	if status.RecoveryStatus != RecoveryStatusIdle {
		t.Fatalf("recovery status = %q, want idle", status.RecoveryStatus)
	}
	if len(events) < 2 || events[0].Status != RecoveryStatusRunning || events[1].Status != RecoveryStatusIdle {
		t.Fatalf("recovery events = %#v", events)
	}
	checkpoint, err := store.GetSchedulerRecoveryCheckpoint(context.Background())
	if err != nil || !checkpoint.Exists || checkpoint.CheckpointUnix != now.Unix() {
		t.Fatalf("shutdown checkpoint = %#v, %v", checkpoint, err)
	}
}
