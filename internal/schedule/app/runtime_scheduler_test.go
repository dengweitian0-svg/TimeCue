package app

import (
	"context"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestReminderSchedulerRebuildsAfterSuspendAndResume 验证暂停清空候选、恢复按事件快照严格向后重建。
func TestReminderSchedulerRebuildsAfterSuspendAndResume(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	first := time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	cron := "0 9 * * *"
	sources := &schedulerTestSources{items: []OccurrenceSourceDTO{{
		Schedule: domain.Schedule{ID: 1, Open: true, Cron: &cron, FirstStartLocal: first, OriginalStartLocal: first, DurationMinutes: 10, UntilDateLocal: strptr("2026-01-05")},
	}}}
	clock := &schedulerTestClock{snapshot: domain.TimeSnapshot{Now: first.Add(-time.Minute), Location: location}}
	scheduler := NewReminderScheduler(sources, &schedulerTestAttempts{claimed: map[domain.OccurrenceIdentity]bool{}, outcomes: map[domain.OccurrenceIdentity]ReminderAttemptStatus{}}, &schedulerTestNotifier{}, clock, nil, nil, NewMutationCoordinator())
	if err := scheduler.refresh(context.Background()); err != nil {
		t.Fatalf("initial refresh error = %v", err)
	}
	if len(scheduler.candidates) != 1 {
		t.Fatalf("initial candidates = %+v", scheduler.candidates)
	}
	if err := scheduler.ApplyRuntimeEvent(context.Background(), RuntimeEvent{Type: RuntimeEventSuspend}); err != nil {
		t.Fatalf("suspend error = %v", err)
	}
	if status := scheduler.GetRuntimeStatus(); status.State != RuntimeStateSuspended || status.Available || len(scheduler.candidates) != 0 {
		t.Fatalf("suspended status=%+v candidates=%+v", status, scheduler.candidates)
	}
	resumeNow := first.Add(2 * time.Minute)
	if err := scheduler.ApplyRuntimeEvent(context.Background(), RuntimeEvent{
		Type:     RuntimeEventResume,
		Snapshot: domain.TimeSnapshot{Now: resumeNow, Location: location},
	}); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	status := scheduler.GetRuntimeStatus()
	if status.State != RuntimeStateRunning || !status.Available {
		t.Fatalf("resumed status = %+v", status)
	}
	candidate, ok := scheduler.nextCandidate()
	if !ok || !candidate.batch.Occurrences[0].StartLocal.After(resumeNow) {
		t.Fatalf("resumed candidate = %+v, now=%v", candidate, resumeNow)
	}
}

// TestReminderSchedulerRejectsEnvironmentAnomalyBeforeSubmit 验证到期回调发现环境异常时不占用身份并重建。
func TestReminderSchedulerRejectsEnvironmentAnomalyBeforeSubmit(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	sources := &schedulerTestSources{items: []OccurrenceSourceDTO{{
		Schedule: domain.Schedule{ID: 2, Open: true, FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: 10},
	}}}
	clock := &schedulerTestClock{snapshot: domain.TimeSnapshot{Now: start.Add(-time.Minute), Location: location}}
	attempts := &schedulerTestAttempts{claimed: map[domain.OccurrenceIdentity]bool{}, outcomes: map[domain.OccurrenceIdentity]ReminderAttemptStatus{}}
	notifier := &schedulerTestNotifier{}
	scheduler := NewReminderScheduler(sources, attempts, notifier, clock, nil, nil, NewMutationCoordinator())
	if err := scheduler.refresh(context.Background()); err != nil {
		t.Fatalf("initial refresh error = %v", err)
	}
	candidate, ok := scheduler.nextCandidate()
	if !ok {
		t.Fatal("initial candidate missing")
	}
	clock.mu.Lock()
	clock.snapshot = domain.TimeSnapshot{Now: start, Location: location, SleepDuration: time.Hour, WallClockJump: time.Hour, TimeZoneChanged: true}
	clock.mu.Unlock()
	if err := scheduler.processDue(context.Background(), candidate); err != nil {
		t.Fatalf("processDue() error = %v", err)
	}
	if len(notifier.items) != 0 || len(attempts.claimed) != 0 {
		t.Fatalf("anomalous delivery submitted=%+v claimed=%+v", notifier.items, attempts.claimed)
	}
	if _, ok := scheduler.nextCandidate(); ok {
		t.Fatalf("anomalous candidate should be rebuilt strictly after the observed now: %+v", scheduler.candidates)
	}
}

type runtimeSequenceClock struct {
	snapshots []domain.TimeSnapshot
	index     int
}

// Snapshot 按预设顺序返回环境事实，用于构造发送前复核期间的异常交错。
func (c *runtimeSequenceClock) Snapshot(context.Context) (domain.TimeSnapshot, error) {
	snapshot := c.snapshots[c.index]
	if c.index < len(c.snapshots)-1 {
		c.index++
	}
	return snapshot, nil
}

// TestReminderSchedulerRechecksEnvironmentBeforeNotification 验证发送前的第二次快照也能阻止误发。
func TestReminderSchedulerRechecksEnvironmentBeforeNotification(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	beforeStart := domain.TimeSnapshot{Now: start.Add(-time.Minute), Location: location}
	ordinary := domain.TimeSnapshot{Now: start, Location: location}
	anomalous := ordinary
	anomalous.WallClockJump = 30 * time.Minute
	sources := &schedulerTestSources{items: []OccurrenceSourceDTO{{
		Schedule: domain.Schedule{ID: 4, Open: true, FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: 10},
	}}}
	clock := &runtimeSequenceClock{snapshots: []domain.TimeSnapshot{beforeStart, ordinary, anomalous}}
	attempts := &schedulerTestAttempts{claimed: map[domain.OccurrenceIdentity]bool{}, outcomes: map[domain.OccurrenceIdentity]ReminderAttemptStatus{}}
	notifier := &schedulerTestNotifier{}
	scheduler := NewReminderScheduler(sources, attempts, notifier, clock, nil, nil, NewMutationCoordinator())
	if err := scheduler.refresh(context.Background()); err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	candidate, ok := scheduler.nextCandidate()
	if !ok {
		t.Fatal("candidate missing")
	}
	if err := scheduler.processDue(context.Background(), candidate); err != nil {
		t.Fatalf("processDue() error = %v", err)
	}
	if len(notifier.items) != 0 || attempts.outcomes[candidate.batch.Occurrences[0].Identity()] != ReminderAttemptSkipped {
		t.Fatalf("notification/outcome = %+v/%+v", notifier.items, attempts.outcomes)
	}
}

type runtimeBlockingNotifier struct {
	started chan struct{}
}

// Submit 等待调度器取消上下文，用于验证运行时停止能打断在途通知。
func (n *runtimeBlockingNotifier) Submit(ctx context.Context, _ ReminderNotification) error {
	close(n.started)
	<-ctx.Done()
	return ctx.Err()
}

// TestReminderSchedulerStopsInFlightNotification 验证 stop 事件取消通知并将已占用身份回写为 skipped。
func TestReminderSchedulerStopsInFlightNotification(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	sources := &schedulerTestSources{items: []OccurrenceSourceDTO{{
		Schedule: domain.Schedule{ID: 3, Open: true, FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: 10},
	}}}
	clock := &schedulerTestClock{snapshot: domain.TimeSnapshot{Now: start.Add(-time.Minute), Location: location}}
	attempts := &schedulerTestAttempts{claimed: map[domain.OccurrenceIdentity]bool{}, outcomes: map[domain.OccurrenceIdentity]ReminderAttemptStatus{}}
	notifier := &runtimeBlockingNotifier{started: make(chan struct{})}
	scheduler := NewReminderScheduler(sources, attempts, notifier, clock, nil, nil, NewMutationCoordinator())
	if err := scheduler.refresh(context.Background()); err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	candidate, ok := scheduler.nextCandidate()
	if !ok {
		t.Fatal("candidate missing")
	}
	clock.setNow(start)
	processDone := make(chan error, 1)
	go func() { processDone <- scheduler.processDue(context.Background(), candidate) }()
	select {
	case <-notifier.started:
	case <-time.After(2 * time.Second):
		t.Fatal("notification did not start")
	}
	if err := scheduler.ApplyRuntimeEvent(context.Background(), RuntimeEvent{Type: RuntimeEventStop}); err != nil {
		t.Fatalf("stop error = %v", err)
	}
	if err := <-processDone; err != nil {
		t.Fatalf("processDue() error = %v", err)
	}
	identity := candidate.batch.Occurrences[0].Identity()
	if attempts.outcomes[identity] != ReminderAttemptSkipped {
		t.Fatalf("outcome = %+v, want skipped", attempts.outcomes)
	}
	if status := scheduler.GetRuntimeStatus(); status.State != RuntimeStateStopping || status.Available {
		t.Fatalf("stopped status = %+v", status)
	}
}

// strptr 创建测试用的字符串指针，避免把测试数据构造逻辑混入生产代码。
func strptr(value string) *string { return &value }
