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
