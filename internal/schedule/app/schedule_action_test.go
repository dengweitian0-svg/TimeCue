package app

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestDecodeScheduleActionTracksFieldPresence 验证整组操作请求保留版本字段存在性。
func TestDecodeScheduleActionTracksFieldPresence(t *testing.T) {
	request, err := DecodeScheduleAction([]byte(`{"expected_revision":3}`))
	if err != nil {
		t.Fatalf("DecodeScheduleAction() error = %v", err)
	}
	if request.ExpectedRevision != 3 || !request.ExpectedRevisionPresent {
		t.Fatalf("request = %+v", request)
	}
}

// TestDecodeScheduleActionRejectsInvalidBoundary 验证整组操作 JSON 拒绝 null、数组和非整数版本。
func TestDecodeScheduleActionRejectsInvalidBoundary(t *testing.T) {
	for _, input := range []string{"null", "[]", `{"expected_revision":1.0}`} {
		_, err := DecodeScheduleAction([]byte(input))
		var businessErr *domain.Error
		if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
			t.Errorf("DecodeScheduleAction(%q) error = %#v, want validation", input, err)
		}
	}
}

// TestEncodeScheduleActionUsesControlledFields 验证整组动作 DTO 只输出四个公共字段。
func TestEncodeScheduleActionUsesControlledFields(t *testing.T) {
	data, err := EncodeScheduleAction(ScheduleActionDTO{
		ScheduleID: 1, Open: false, Revision: 4, UpdatedAtUTC: time.Date(2026, 9, 12, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("EncodeScheduleAction() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(decoded) != 4 {
		t.Fatalf("encoded fields = %#v", decoded)
	}
	for _, field := range []string{"schedule_id", "open", "revision", "updated_at_utc"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("encoded result missing %q: %s", field, data)
		}
	}
}

// TestEncodeOccurrenceActionIncludesScheduleOpen 验证例外动作结果回流整组有效状态。
func TestEncodeOccurrenceActionIncludesScheduleOpen(t *testing.T) {
	data, err := EncodeOccurrenceAction(OccurrenceActionDTO{ScheduleID: 1, OriginalDate: "2026-09-07", ScheduleOpen: false})
	if err != nil || !containsJSONField(data, "schedule_open") {
		t.Fatalf("encoded = %s, error = %v", data, err)
	}
}

// containsJSONField 判断 JSON 对象是否包含指定字段。
func containsJSONField(data []byte, field string) bool {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return false
	}
	_, ok := decoded[field]
	return ok
}
