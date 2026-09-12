package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	json "encoding/json/v2"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestDecodeCreateScheduleKeepsZeroPointer 验证零值时间通过指针与缺失字段区分。
func TestDecodeCreateScheduleKeepsZeroPointer(t *testing.T) {
	request, err := DecodeCreateSchedule([]byte(`{"title":"zero","start_unix":0,"end_unix":60}`))
	if err != nil {
		t.Fatalf("DecodeCreateSchedule() error = %v", err)
	}
	if request.StartUnix == nil || *request.StartUnix != 0 {
		t.Fatalf("StartUnix = %v, want pointer to 0", request.StartUnix)
	}
	if request.EndUnix == nil || *request.EndUnix != 60 {
		t.Fatalf("EndUnix = %v, want pointer to 60", request.EndUnix)
	}
}

// TestDecodeCreateScheduleRejectsInvalidNumbers 验证时间字段拒绝非整数 JSON 表示。
func TestDecodeCreateScheduleRejectsInvalidNumbers(t *testing.T) {
	inputs := []string{
		`{"start_unix":1.0,"end_unix":60}`,
		`{"start_unix":1e2,"end_unix":60}`,
		`{"start_unix":"1","end_unix":60}`,
		`{"start_unix":true,"end_unix":60}`,
		`{"start_unix":[],"end_unix":60}`,
		`{"start_unix":9223372036854775808,"end_unix":60}`,
		`{"start_unix":1,"end_unix":60} trailing`,
		`null`,
	}
	for _, input := range inputs {
		t.Run(strings.ReplaceAll(input, "\"", ""), func(t *testing.T) {
			_, err := DecodeCreateSchedule([]byte(input))
			if err == nil {
				t.Fatalf("DecodeCreateSchedule(%q) error = nil", input)
			}
			var businessErr *domain.Error
			if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
				t.Fatalf("error = %#v, want validation error", err)
			}
		})
	}
}

// TestDecodeUpdateScheduleTracksFieldPresence 验证更新 JSON 能区分缺失字段和显式携带字段。
func TestDecodeUpdateScheduleTracksFieldPresence(t *testing.T) {
	request, err := DecodeUpdateSchedule([]byte(`{"cron":null,"cancel":false,"expected_revision":3}`))
	if err != nil {
		t.Fatalf("DecodeUpdateSchedule() error = %v", err)
	}
	if !request.CronPresent || request.Cron != nil {
		t.Fatalf("cron presence/value = %v/%v", request.CronPresent, request.Cron)
	}
	if !request.CancelPresent || request.Cancel == nil || *request.Cancel {
		t.Fatalf("cancel presence/value = %v/%v", request.CancelPresent, request.Cancel)
	}
	if !request.ExpectedRevisionPresent || request.ExpectedRevision != 3 {
		t.Fatalf("expected revision = %d/%v", request.ExpectedRevision, request.ExpectedRevisionPresent)
	}
}

// TestDecodeUpdateScheduleRejectsInvalidNumbers 验证更新 JSON 的端点和版本拒绝非整数表示。
func TestDecodeUpdateScheduleRejectsInvalidNumbers(t *testing.T) {
	for _, input := range []string{
		`{"start_unix":1.0,"expected_revision":1}`,
		`{"end_unix":"60","expected_revision":1}`,
		`{"expected_revision":1.0}`,
		`null`,
	} {
		_, err := DecodeUpdateSchedule([]byte(input))
		var businessErr *domain.Error
		if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
			t.Errorf("DecodeUpdateSchedule(%q) error = %#v, want validation", input, err)
		}
	}
}

// TestEncodeListSchedulesOmitsEmptyCursor 验证分页结束时不输出空游标。
func TestEncodeListSchedulesOmitsEmptyCursor(t *testing.T) {
	data, err := EncodeListSchedules(ListSchedulesResponse{Items: []ScheduleDTO{{ID: 1, Title: "one"}}})
	if err != nil {
		t.Fatalf("EncodeListSchedules() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := decoded["next_cursor"]; ok {
		t.Fatalf("encoded response contains empty next_cursor: %s", data)
	}
}

// TestEncodeErrorIncludesBoundsWithoutCause 验证业务错误输出字段和边界但不泄露底层原因。
func TestEncodeErrorIncludesBoundsWithoutCause(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	snapshot := domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 12, 0, 0, 0, location), Location: location}
	cause := errors.New("database detail")
	err := &domain.Error{
		Code:    domain.TimeRangeExceeded,
		Message: "outside range",
		Fields:  []string{"start_unix"},
		Bounds:  ptr(domain.CalculateTimeBounds(snapshot)),
		Cause:   cause,
	}
	data, encodeErr := EncodeError(err)
	if encodeErr != nil {
		t.Fatalf("EncodeError() error = %v", encodeErr)
	}
	if strings.Contains(string(data), cause.Error()) || !strings.Contains(string(data), `"code":"TIME_RANGE_EXCEEDED"`) {
		t.Fatalf("encoded error = %s", data)
	}
}

// ptr 返回测试所需的时间边界指针。
func ptr(value domain.TimeBounds) *domain.TimeBounds {
	return &value
}
