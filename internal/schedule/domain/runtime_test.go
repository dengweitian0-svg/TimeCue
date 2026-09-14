package domain

import (
	"errors"
	"testing"
	"time"
)

// TestTimeSnapshotEnvironmentFacts 验证时间快照能准确表达提醒重建所需的环境事实。
func TestTimeSnapshotEnvironmentFacts(t *testing.T) {
	base := TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC), Location: time.UTC}
	for name, snapshot := range map[string]TimeSnapshot{
		"ordinary": base,
		"sleep":    func() TimeSnapshot { value := base; value.SleepDuration = 2 * time.Hour; return value }(),
		"clock":    func() TimeSnapshot { value := base; value.WallClockJump = -30 * time.Minute; return value }(),
		"timezone": func() TimeSnapshot { value := base; value.TimeZoneChanged = true; return value }(),
	} {
		want := name != "ordinary"
		if got := snapshot.HasEnvironmentChange(); got != want {
			t.Errorf("%s HasEnvironmentChange() = %v, want %v", name, got, want)
		}
	}
}

// TestNewRuntimeStoppingPreservesBusinessClassification 验证停止准入错误保留机器可读分类。
func TestNewRuntimeStoppingPreservesBusinessClassification(t *testing.T) {
	err := NewRuntimeStopping()
	var businessErr *Error
	if !errors.As(err, &businessErr) {
		t.Fatalf("error does not preserve domain error: %v", err)
	}
	if businessErr.Code != RuntimeStopping {
		t.Fatalf("code = %q, want %q", businessErr.Code, RuntimeStopping)
	}
}
