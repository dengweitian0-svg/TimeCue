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
