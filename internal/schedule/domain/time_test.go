package domain

import (
	"errors"
	"testing"
	"time"
)

// TestCalculateTimeBoundsUsesLocalDate 验证边界使用本地日期而非 UTC 日期。
func TestCalculateTimeBoundsUsesLocalDate(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	snapshot := TimeSnapshot{Now: time.Date(2026, 1, 1, 0, 30, 0, 0, location), Location: location}
	bounds := CalculateTimeBounds(snapshot)
	wantMin := time.Date(2026, 1, 1, 0, 0, 0, 0, location)
	if !bounds.SingleMin.Equal(wantMin) {
		t.Fatalf("SingleMin = %v, want %v", bounds.SingleMin, wantMin)
	}
	if !bounds.SingleMaxExclusive.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, location)) {
		t.Fatalf("SingleMaxExclusive = %v", bounds.SingleMaxExclusive)
	}
	if !bounds.QueryMaxExclusive.Equal(time.Date(2027, 1, 2, 0, 0, 0, 0, location)) {
		t.Fatalf("QueryMaxExclusive = %v", bounds.QueryMaxExclusive)
	}
}

// TestAddCalendarYearClampedKeepsLeapDayBoundary 验证闰日加一年时夹取到二月二十八日。
func TestAddCalendarYearClampedKeepsLeapDayBoundary(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	value := time.Date(2024, time.February, 29, 12, 34, 56, 123, location)
	want := time.Date(2025, time.February, 28, 12, 34, 56, 123, location)
	if got := AddCalendarYearClamped(value); !got.Equal(want) {
		t.Fatalf("AddCalendarYearClamped() = %v, want %v", got, want)
	}
}

// TestValidateSingleEndpointsRejectsInvalidEndpoints 验证分钟精度、顺序和时间范围均被严格执行。
func TestValidateSingleEndpointsRejectsInvalidEndpoints(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	snapshot := TimeSnapshot{Now: time.Date(2026, 1, 1, 12, 0, 0, 0, location), Location: location}
	cases := []struct {
		name     string
		start    int64
		end      int64
		wantCode Code
	}{
		{name: "start seconds", start: snapshot.Now.Unix() + 1, end: snapshot.Now.Add(time.Hour).Unix(), wantCode: Validation},
		{name: "end seconds", start: snapshot.Now.Unix(), end: snapshot.Now.Add(time.Hour).Unix() + 1, wantCode: Validation},
		{name: "reversed", start: snapshot.Now.Add(time.Hour).Unix(), end: snapshot.Now.Unix(), wantCode: Validation},
		{name: "before local day", start: time.Date(2025, 12, 31, 23, 59, 0, 0, location).Unix(), end: snapshot.Now.Unix(), wantCode: TimeRangeExceeded},
		{name: "after max", start: time.Date(2027, 1, 1, 0, 0, 0, 0, location).Unix(), end: time.Date(2027, 1, 1, 0, 30, 0, 0, location).Unix(), wantCode: TimeRangeExceeded},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ValidateSingleEndpoints(snapshot, testCase.start, testCase.end)
			if err == nil {
				t.Fatal("ValidateSingleEndpoints() error = nil")
			}
			var businessErr *Error
			if !errors.As(err, &businessErr) {
				t.Fatalf("error type = %T, want *Error", err)
			}
			if businessErr.Code != testCase.wantCode {
				t.Fatalf("error code = %s, want %s", businessErr.Code, testCase.wantCode)
			}
		})
	}
}

// TestValidateSingleEndpointsUsesUTCForCreationTimes 验证候选的审计时间统一保存为 UTC。
func TestValidateSingleEndpointsUsesUTCForCreationTimes(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, location)
	candidate, err := ValidateSingleEndpoints(TimeSnapshot{Now: now, Location: location}, now.Unix(), now.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("ValidateSingleEndpoints() error = %v", err)
	}
	if candidate.CreatedAtUTC.Location() != time.UTC || candidate.UpdatedAtUTC.Location() != time.UTC {
		t.Fatalf("audit locations = %v, %v; want UTC", candidate.CreatedAtUTC.Location(), candidate.UpdatedAtUTC.Location())
	}
}

// TestValidateSingleEndpointsHonorsExactRangeBoundaries 验证下界包含、开始上界排他且结束端点可落在上界。
func TestValidateSingleEndpointsHonorsExactRangeBoundaries(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	snapshot := TimeSnapshot{Now: time.Date(2026, 1, 1, 12, 0, 0, 0, location), Location: location}
	bounds := CalculateTimeBounds(snapshot)
	for _, testCase := range []struct {
		name       string
		start, end time.Time
	}{
		{name: "single minimum", start: bounds.SingleMin, end: bounds.SingleMin.Add(30 * time.Minute)},
		{name: "single maximum as end", start: bounds.SingleMaxExclusive.Add(-30 * time.Minute), end: bounds.SingleMaxExclusive},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate, err := ValidateSingleEndpoints(snapshot, testCase.start.Unix(), testCase.end.Unix())
			if err != nil {
				t.Fatalf("ValidateSingleEndpoints() error = %v", err)
			}
			if candidate.DurationMinutes != 30 {
				t.Fatalf("duration = %d, want 30", candidate.DurationMinutes)
			}
		})
	}

	_, err := ValidateSingleEndpoints(snapshot, bounds.SingleMaxExclusive.Unix(), bounds.SingleMaxExclusive.Add(30*time.Minute).Unix())
	var businessErr *Error
	if !errors.As(err, &businessErr) || businessErr.Code != TimeRangeExceeded {
		t.Fatalf("start at exclusive maximum error = %#v, want time range error", err)
	}
}

// TestValidateSingleEndpointsCalculatesDurationAcrossDST 验证跨 DST 端点按实际 Unix 时长而非墙钟差计算持续时间。
func TestValidateSingleEndpointsCalculatesDurationAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := TimeSnapshot{Now: time.Date(2026, 3, 7, 12, 0, 0, 0, location), Location: location}
	start := time.Date(2026, 3, 8, 1, 30, 0, 0, location)
	end := time.Date(2026, 3, 8, 4, 30, 0, 0, location)
	candidate, err := ValidateSingleEndpoints(snapshot, start.Unix(), end.Unix())
	if err != nil {
		t.Fatalf("ValidateSingleEndpoints() error = %v", err)
	}
	if candidate.DurationMinutes != 120 {
		t.Fatalf("duration = %d, want 120 actual minutes", candidate.DurationMinutes)
	}
	if candidate.FirstStartLocal.Format(time.RFC3339) != "2026-03-08T01:30:00-05:00" {
		t.Fatalf("start = %s, want pre-DST offset", candidate.FirstStartLocal.Format(time.RFC3339))
	}
}
