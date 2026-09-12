package domain

import (
	"testing"
	"time"
)

// TestScheduleCandidateKeepsOriginalStart 验证候选同时保存首次和原始开始时间。
func TestScheduleCandidateKeepsOriginalStart(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	snapshot := TimeSnapshot{Now: time.Date(2026, 1, 2, 8, 0, 0, 0, location), Location: location}
	candidate, err := ValidateSingleEndpoints(snapshot, snapshot.Now.Add(time.Hour).Unix(), snapshot.Now.Add(2*time.Hour).Unix())
	if err != nil {
		t.Fatalf("ValidateSingleEndpoints() error = %v", err)
	}
	if candidate.Cron != nil || !candidate.Open {
		t.Fatalf("single candidate defaults = cron %v, open %v", candidate.Cron, candidate.Open)
	}
	if !candidate.FirstStartLocal.Equal(candidate.OriginalStartLocal) {
		t.Fatalf("start fields differ: %v != %v", candidate.FirstStartLocal, candidate.OriginalStartLocal)
	}
	if candidate.DurationMinutes != 60 {
		t.Fatalf("DurationMinutes = %d, want 60", candidate.DurationMinutes)
	}
}

// TestScheduleUpdateCandidateDistinguishesMissingCron 验证 cron 的存在性不由值是否为 nil 决定。
func TestScheduleUpdateCandidateDistinguishesMissingCron(t *testing.T) {
	var candidate ScheduleUpdateCandidate
	if candidate.CronPresent {
		t.Fatal("zero update candidate unexpectedly carries cron")
	}
	candidate.CronPresent = true
	if candidate.Cron != nil {
		t.Fatalf("explicit nil cron = %v, want nil value", candidate.Cron)
	}
	if !candidate.CronPresent {
		t.Fatal("explicit cron presence was lost")
	}
}

// TestScheduleIsRecurringUsesCronPresence 验证日程类型由周期规则是否存在决定。
func TestScheduleIsRecurringUsesCronPresence(t *testing.T) {
	if (Schedule{}).IsRecurring() {
		t.Fatal("single schedule reported as recurring")
	}
	cron := "0 9 * * *"
	if !(Schedule{Cron: &cron}).IsRecurring() {
		t.Fatal("recurring schedule reported as single")
	}
}
