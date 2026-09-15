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

// TestDecodePreviewChangeTracksNestedCronPresence 验证预览载荷中的显式 null cron 不会丢失类型转换语义。
func TestDecodePreviewChangeTracksNestedCronPresence(t *testing.T) {
	request, err := DecodePreviewChange([]byte(`{"operation":"update_schedule","schedule_update":{"cron":null}}`))
	if err != nil {
		t.Fatalf("DecodePreviewChange() error = %v", err)
	}
	if request.ScheduleUpdate == nil || !request.ScheduleUpdate.CronPresent || request.ScheduleUpdate.Cron != nil {
		t.Fatalf("nested cron presence/value = %#v", request.ScheduleUpdate)
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

// TestDecodeDeleteScheduleTracksExpectedRevision 验证删除 JSON 保留版本字段存在性。
func TestDecodeDeleteScheduleTracksExpectedRevision(t *testing.T) {
	request, err := DecodeDeleteSchedule([]byte(`{"expected_revision":2}`))
	if err != nil {
		t.Fatalf("DecodeDeleteSchedule() error = %v", err)
	}
	if !request.ExpectedRevisionPresent || request.ExpectedRevision != 2 {
		t.Fatalf("delete request = %#v", request)
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

// TestEncodeScheduleIncludesMonthEndPolicy 验证策略在 DTO 中统一输出字符串或 null。
func TestEncodeScheduleIncludesMonthEndPolicy(t *testing.T) {
	policy := domain.MonthEndPolicyClamp
	for _, tc := range []struct {
		name   string
		value  *string
		wanted string
	}{
		{name: "clamp", value: &policy, wanted: `"month_end_policy":"clamp"`},
		{name: "null", value: nil, wanted: `"month_end_policy":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := EncodeSchedule(ScheduleDTO{MonthEndPolicy: tc.value})
			if err != nil || !strings.Contains(string(data), tc.wanted) {
				t.Fatalf("encoded = %s, error = %v", data, err)
			}
		})
	}
}

// TestEncodeScheduleIncludesCancelledOccurrences 验证单条日程响应暴露取消例外的受控字段。
func TestEncodeScheduleIncludesCancelledOccurrences(t *testing.T) {
	start, end := int64(10), int64(70)
	title, notes := "moved", "notes"
	data, err := EncodeSchedule(ScheduleDTO{CancelledOccurrences: []CancelledOccurrenceDTO{{
		OriginalDate: "2026-09-07", SnapshotPresent: true,
		StartUnix: &start, EndUnix: &end, Title: &title, Notes: &notes,
	}}})
	if err != nil {
		t.Fatalf("EncodeSchedule() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	items, ok := decoded["cancelled_occurrences"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("cancelled_occurrences = %#v", decoded["cancelled_occurrences"])
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
