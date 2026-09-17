package windows

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
)

// TestEnvironmentClockLifecycleIsIdempotent 验证重复挂起、无对应恢复和关闭回调的幂等行为。
func TestEnvironmentClockLifecycleIsIdempotent(t *testing.T) {
	now := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	clock, err := NewTestEnvironmentClock(EnvironmentClockOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewTestEnvironmentClock() error = %v", err)
	}
	if err := clock.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := clock.OnResume(); err != nil {
		t.Fatalf("resume without suspend error = %v", err)
	}
	if err := clock.OnSuspend(); err != nil {
		t.Fatalf("suspend error = %v", err)
	}
	if err := clock.OnSuspend(); err != nil {
		t.Fatalf("repeated suspend error = %v", err)
	}
	now = now.Add(2 * time.Hour)
	if err := clock.OnResume(); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if err := clock.OnResume(); err != nil {
		t.Fatalf("repeated resume error = %v", err)
	}
	events := make([]app.RuntimeEvent, 0, 2)
	for range 2 {
		events = append(events, <-clock.Events())
	}
	if err := clock.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	wantTypes := []app.RuntimeEventType{app.RuntimeEventSuspend, app.RuntimeEventResume}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %+v, want %d idempotent events", events, len(wantTypes))
	}
	for index, want := range wantTypes {
		if events[index].Type != want {
			t.Fatalf("events[%d].Type = %q, want %q", index, events[index].Type, want)
		}
	}
	if events[1].Snapshot.SleepDuration != 2*time.Hour {
		t.Fatalf("resume sleep duration = %s, want 2h", events[1].Snapshot.SleepDuration)
	}
}

// TestEnvironmentClockRefreshesBeforeEvent 验证时钟/时区变化事件携带刷新后的同一份快照。
func TestEnvironmentClockRefreshesBeforeEvent(t *testing.T) {
	now := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	location := time.FixedZone("before", 8*60*60)
	updated := time.FixedZone("after", 9*60*60)
	currentLocation := location
	clock, err := NewTestEnvironmentClock(EnvironmentClockOptions{
		Now:          func() time.Time { return now },
		LoadLocation: func() (*time.Location, error) { return currentLocation, nil },
	})
	if err != nil {
		t.Fatalf("NewTestEnvironmentClock() error = %v", err)
	}
	if err := clock.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	now = now.Add(time.Minute)
	if err := clock.NotifyClockChanged(); err != nil {
		t.Fatalf("NotifyClockChanged() error = %v", err)
	}
	currentLocation = updated
	now = now.Add(time.Minute)
	if err := clock.NotifyTimeZoneChanged(); err != nil {
		t.Fatalf("NotifyTimeZoneChanged() error = %v", err)
	}
	first := <-clock.Events()
	second := <-clock.Events()
	if first.Snapshot.Now != now.Add(-time.Minute) || first.Snapshot.Location != location || first.Snapshot.WallClockJump != time.Minute {
		t.Fatalf("clock event snapshot = %+v, want refreshed wall-clock snapshot", first.Snapshot)
	}
	if second.Snapshot.Now != now || second.Snapshot.Location != updated || !second.Snapshot.TimeZoneChanged {
		t.Fatalf("timezone event snapshot = %+v, want refreshed timezone snapshot", second.Snapshot)
	}
	if err := clock.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestEnvironmentClockSnapshotSamplesCurrentTime 验证普通快照不会冻结在上一次系统事件时刻。
func TestEnvironmentClockSnapshotSamplesCurrentTime(t *testing.T) {
	now := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	clock, err := NewTestEnvironmentClock(EnvironmentClockOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewTestEnvironmentClock() error = %v", err)
	}
	if err := clock.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	first, err := clock.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("first Snapshot() error = %v", err)
	}
	now = now.Add(90 * time.Second)
	second, err := clock.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("second Snapshot() error = %v", err)
	}
	if second.Now.Sub(first.Now) != 90*time.Second {
		t.Fatalf("snapshot delta = %s, want 90s", second.Now.Sub(first.Now))
	}
	select {
	case event := <-clock.Events():
		t.Fatalf("unexpected runtime event without environment change: %+v", event)
	default:
	}
	if err := clock.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestEnvironmentClockRejectsCallbacksAfterClose 验证关闭后旧 epoch 回调不能再产生事件。
func TestEnvironmentClockRejectsCallbacksAfterClose(t *testing.T) {
	clock, err := NewTestEnvironmentClock(EnvironmentClockOptions{})
	if err != nil {
		t.Fatalf("NewTestEnvironmentClock() error = %v", err)
	}
	if err := clock.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := clock.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := clock.NotifyClockChanged(); err == nil {
		t.Fatal("NotifyClockChanged() error = nil after Close()")
	}
	if err := clock.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, ok := <-clock.Events(); ok {
		t.Fatal("Events() channel is open after Close()")
	}
}

// TestBuildLocationFromRulesHandlesFutureDST 验证生成的 TZif 对未来年份仍执行夏令时转换。
func TestBuildLocationFromRulesHandlesFutureDST(t *testing.T) {
	rule := TimeZoneRule{
		Name:         "Test/Dynamic",
		Bias:         480,
		DaylightDate: SystemTimeRule{Month: 3, DayOfWeek: 0, Day: 2, Hour: 2},
		StandardDate: SystemTimeRule{Month: 11, DayOfWeek: 0, Day: 1, Hour: 2},
		DaylightBias: -60,
	}
	rules := map[int]TimeZoneRule{2026: rule, 2027: rule, 2028: rule, 2200: rule}
	location, err := BuildLocationFromRules(rule.Name, rules, 2026, 2200)
	if err != nil {
		t.Fatalf("BuildLocationFromRules() error = %v", err)
	}
	standard := time.Date(2120, 1, 15, 12, 0, 0, 0, location)
	daylight := time.Date(2120, 7, 15, 12, 0, 0, 0, location)
	_, standardOffset := standard.Zone()
	_, daylightOffset := daylight.Zone()
	if standardOffset != -8*60*60 || daylightOffset != -7*60*60 {
		t.Fatalf("future offsets = %d and %d, want -8h and -7h", standardOffset, daylightOffset)
	}
}

// TestBuildLocationFromRulesRejectsInvalidRange 验证动态规则生成器拒绝空规则和倒置范围。
func TestBuildLocationFromRulesRejectsInvalidRange(t *testing.T) {
	if _, err := BuildLocationFromRules("test", nil, 2026, 2027); err == nil {
		t.Fatal("BuildLocationFromRules() error = nil for empty rules")
	}
	if _, err := BuildLocationFromRules("test", map[int]TimeZoneRule{2026: {}}, 2027, 2026); err == nil {
		t.Fatal("BuildLocationFromRules() error = nil for reversed range")
	}
}

// TestEnvironmentClockPropagatesLocationFailure 验证注入的时区读取错误保留原始 cause。
func TestEnvironmentClockPropagatesLocationFailure(t *testing.T) {
	wantErr := errors.New("time zone unavailable")
	if _, err := NewTestEnvironmentClock(EnvironmentClockOptions{LoadLocation: func() (*time.Location, error) { return nil, wantErr }}); !errors.Is(err, wantErr) {
		t.Fatalf("NewTestEnvironmentClock() error = %v, want %v", err, wantErr)
	}
}
