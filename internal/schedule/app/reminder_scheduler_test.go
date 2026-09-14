package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

type schedulerTestClock struct {
	mu       sync.Mutex
	snapshot domain.TimeSnapshot
}

// Snapshot 返回测试可推进的时间快照。
func (c *schedulerTestClock) Snapshot(context.Context) (domain.TimeSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot, nil
}

// setNow 推进测试时钟。
func (c *schedulerTestClock) setNow(now time.Time) { c.mu.Lock(); c.snapshot.Now = now; c.mu.Unlock() }

type schedulerTestTimer struct {
	ch chan time.Time
}

// C 返回可控计时器通道。
func (t *schedulerTestTimer) C() <-chan time.Time { return t.ch }

// Stop 保持测试计时器可重置。
func (*schedulerTestTimer) Stop() bool { return true }

// Reset 保持测试计时器可重置。
func (t *schedulerTestTimer) Reset(duration time.Duration) bool {
	if duration <= 0 {
		select {
		case t.ch <- time.Time{}:
		default:
		}
	}
	return true
}

type schedulerTestSources struct {
	mu      sync.Mutex
	items   []OccurrenceSourceDTO
	created int
}

// ListOccurrenceSources 返回全部测试事实。
func (s *schedulerTestSources) ListOccurrenceSources(_ context.Context, request ListOccurrenceSourcesRequest) (ListOccurrenceSourcesResponse, error) {
	if request.AfterScheduleID != 0 {
		return ListOccurrenceSourcesResponse{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return ListOccurrenceSourcesResponse{Items: append([]OccurrenceSourceDTO(nil), s.items...)}, nil
}

// GetOccurrenceSource 返回指定日程的当前测试事实。
func (s *schedulerTestSources) GetOccurrenceSource(_ context.Context, id domain.ScheduleID) (OccurrenceSourceDTO, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.Schedule.ID == id {
			return item, nil
		}
	}
	return OccurrenceSourceDTO{}, domain.NewNotFound("schedule not found")
}

// QueryGeneration 满足调度器复用的查询源端口；提醒重建不依赖持久化 generation。
func (*schedulerTestSources) QueryGeneration(context.Context) (uint64, error) { return 1, nil }

type schedulerTestAttempts struct {
	mu       sync.Mutex
	claimed  map[domain.OccurrenceIdentity]bool
	outcomes map[domain.OccurrenceIdentity]ReminderAttemptStatus
}

// ClaimReminderAttempt 记录测试占用并拒绝重复身份。
func (s *schedulerTestAttempts) ClaimReminderAttempt(_ context.Context, identity domain.OccurrenceIdentity, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed[identity] {
		return false, nil
	}
	s.claimed[identity] = true
	return true, nil
}

// SetReminderAttemptStatus 记录测试终态。
func (s *schedulerTestAttempts) SetReminderAttemptStatus(_ context.Context, identity domain.OccurrenceIdentity, status ReminderAttemptStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes[identity] = status
	return nil
}

type schedulerTestNotifier struct {
	mu    sync.Mutex
	items []ReminderNotification
}

// Submit 记录测试通知。
func (s *schedulerTestNotifier) Submit(_ context.Context, notification ReminderNotification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, notification)
	return nil
}

// TestReminderSchedulerUsesOneTimerAndProcessesSameTimeBatch 验证全局单计时器和同刻身份逐条消费。
func TestReminderSchedulerUsesOneTimerAndProcessesSameTimeBatch(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 1, 1, 8, 59, 0, 0, location)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	items := []OccurrenceSourceDTO{
		{Schedule: domain.Schedule{ID: 1, Open: true, FirstStartLocal: start, DurationMinutes: 10}},
		{Schedule: domain.Schedule{ID: 2, Open: true, FirstStartLocal: start, DurationMinutes: 20}},
	}
	sources := &schedulerTestSources{items: items}
	clock := &schedulerTestClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}}
	attempts := &schedulerTestAttempts{claimed: make(map[domain.OccurrenceIdentity]bool), outcomes: make(map[domain.OccurrenceIdentity]ReminderAttemptStatus)}
	notifier := &schedulerTestNotifier{}
	var timerMu sync.Mutex
	var timer *schedulerTestTimer
	timerCount := 0
	factory := func(time.Duration) Timer {
		timerMu.Lock()
		defer timerMu.Unlock()
		timerCount++
		timer = &schedulerTestTimer{ch: make(chan time.Time, 1)}
		return timer
	}
	scheduler := NewReminderScheduler(sources, attempts, notifier, clock, factory, discardReminderLogger{}, NewMutationCoordinator())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	deadline := time.After(2 * time.Second)
	for {
		timerMu.Lock()
		ready := timer != nil
		currentTimer := timer
		timerMu.Unlock()
		if ready {
			clock.setNow(start)
			currentTimer.ch <- start
			break
		}
		select {
		case <-deadline:
			t.Fatal("scheduler did not create timer")
		case <-time.After(time.Millisecond):
		}
	}
	deadline = time.After(2 * time.Second)
	for {
		notifier.mu.Lock()
		count := len(notifier.items)
		notifier.mu.Unlock()
		if count == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("same-time reminders were not submitted: status=%+v claimed=%+v outcomes=%+v", scheduler.GetRuntimeStatus(), attempts.claimed, attempts.outcomes)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	timerMu.Lock()
	gotTimers := timerCount
	timerMu.Unlock()
	if gotTimers != 1 {
		t.Fatalf("timer factory calls = %d, want 1", gotTimers)
	}
	if len(attempts.outcomes) != 2 {
		t.Fatalf("outcomes = %+v", attempts.outcomes)
	}
}
