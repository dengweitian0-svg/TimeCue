package app

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestDecodeOccurrenceActionTracksFieldPresence 验证取消/恢复请求保留字段存在性。
func TestDecodeOccurrenceActionTracksFieldPresence(t *testing.T) {
	request, err := DecodeOccurrenceAction([]byte(`{"original_date":"2026-09-07","expected_revision":3}`))
	if err != nil {
		t.Fatalf("DecodeOccurrenceAction() error = %v", err)
	}
	if request.OriginalDate != "2026-09-07" || !request.OriginalDatePresent || request.ExpectedRevision != 3 || !request.ExpectedRevisionPresent {
		t.Fatalf("request = %+v", request)
	}
}

// TestDecodeOccurrenceActionRejectsNonObject 验证取消/恢复边界拒绝非对象 JSON。
func TestDecodeOccurrenceActionRejectsNonObject(t *testing.T) {
	for _, input := range []string{"null", `{"original_date":1}`, `{"expected_revision":1.0}`} {
		_, err := DecodeOccurrenceAction([]byte(input))
		var businessErr *domain.Error
		if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
			t.Errorf("DecodeOccurrenceAction(%q) error = %#v, want validation", input, err)
		}
	}
}

// TestEncodeOccurrenceActionUsesControlledFields 验证操作结果只暴露受控字段。
func TestEncodeOccurrenceActionUsesControlledFields(t *testing.T) {
	start, end := int64(1), int64(2)
	title, notes := "title", "notes"
	data, err := EncodeOccurrenceAction(OccurrenceActionDTO{
		ScheduleID: 1, OriginalDate: "2026-09-07", Cancelled: true, SnapshotPresent: true,
		StartUnix: &start, EndUnix: &end, Title: &title, Notes: &notes, ScheduleRevision: 4,
	})
	if err != nil {
		t.Fatalf("EncodeOccurrenceAction() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, field := range []string{"schedule_id", "original_date", "cancelled", "snapshot_present", "start_unix", "end_unix", "title", "notes", "schedule_revision"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("encoded result missing %q: %s", field, data)
		}
	}
}
