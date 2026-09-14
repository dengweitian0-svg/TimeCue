package app

import (
	"errors"
	"strings"
	"testing"

	json "encoding/json/v2"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestDecodePreviewChangeTracksFieldPresence 验证预览 JSON 保留顶层字段和嵌套载荷存在性。
func TestDecodePreviewChangeTracksFieldPresence(t *testing.T) {
	request, err := DecodePreviewChange([]byte(`{"operation":"update_schedule","schedule_id":3,"expected_revision":2,"schedule_update":{"title":"new"}}`))
	if err != nil {
		t.Fatalf("DecodePreviewChange() error = %v", err)
	}
	if !request.OperationPresent || request.Operation != ChangeOperationUpdateSchedule {
		t.Fatalf("operation = %q/%v", request.Operation, request.OperationPresent)
	}
	if !request.ScheduleIDPresent || request.ScheduleID != 3 || !request.ExpectedRevisionPresent || request.ExpectedRevision != 2 {
		t.Fatalf("identity fields = %+v", request)
	}
	if request.ScheduleUpdate == nil || request.ScheduleUpdate.Title == nil || *request.ScheduleUpdate.Title != "new" {
		t.Fatalf("schedule update payload = %+v", request.ScheduleUpdate)
	}
	if request.OriginalDatePresent || request.Create != nil || request.OccurrenceUpdate != nil {
		t.Fatalf("unexpected payloads in %+v", request)
	}
}

// TestDecodePreviewChangeKeepsZeroIdentityFields 验证零值标识通过存在性标记与缺失字段区分。
func TestDecodePreviewChangeKeepsZeroIdentityFields(t *testing.T) {
	request, err := DecodePreviewChange([]byte(`{"operation":"create","schedule_id":0,"expected_revision":0}`))
	if err != nil {
		t.Fatalf("DecodePreviewChange() error = %v", err)
	}
	if !request.ScheduleIDPresent || request.ScheduleID != 0 || !request.ExpectedRevisionPresent || request.ExpectedRevision != 0 {
		t.Fatalf("zero identity fields = %+v", request)
	}
}

// TestDecodePreviewChangeRejectsInvalidInput 验证非法操作载荷和非对象输入都返回 VALIDATION。
func TestDecodePreviewChangeRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{
		`null`,
		`{} trailing`,
		`{"operation":1}`,
		`{"operation":"create","schedule_id":"1"}`,
		`[]`,
	} {
		t.Run(strings.ReplaceAll(input, `"`, ""), func(t *testing.T) {
			_, err := DecodePreviewChange([]byte(input))
			var businessErr *domain.Error
			if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
				t.Fatalf("DecodePreviewChange(%q) error = %#v, want validation", input, err)
			}
		})
	}
}

// TestEncodeChangePreviewCarriesExplicitStatus 验证预览响应输出明确状态和冲突明细。
func TestEncodeChangePreviewCarriesExplicitStatus(t *testing.T) {
	result := ChangePreviewDTO{
		Operation: ChangeOperationUpdateSchedule,
		Checked:   true,
		HasMore:   true,
		Conflict: ConflictCheckDTO{
			Status: "CHECKED", Checked: true, BatchUnix: 600, OccurrenceCount: 1,
			Target:    &OccurrenceIdentityDTO{ScheduleID: 4, OriginalDate: "2026-01-01", Title: "a", StartUnix: 600, EndUnix: 900},
			Conflicts: []ConflictDTO{{Target: OccurrenceIdentityDTO{ScheduleID: 4, OriginalDate: "2026-01-01"}, Other: OccurrenceIdentityDTO{ScheduleID: 5, OriginalDate: "2026-01-01", Title: "b"}, SameGroup: false, OverlapUnix: 600}},
		},
	}
	data, err := EncodeChangePreview(result)
	if err != nil {
		t.Fatalf("EncodeChangePreview() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	conflict, ok := decoded["conflict"].(map[string]any)
	if !ok || conflict["status"] != "CHECKED" || conflict["checked"] != true {
		t.Fatalf("encoded conflict = %s", data)
	}
	items, ok := conflict["conflicts"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("encoded conflicts = %s", data)
	}
}

// TestEncodeConflictOmitsEmptyFields 验证未检查状态不输出目标批次和冲突明细内容。
func TestEncodeConflictOmitsEmptyFields(t *testing.T) {
	data, err := EncodeChangePreview(ChangePreviewDTO{
		Operation: ChangeOperationRestoreSchedule,
		Conflict:  ConflictCheckDTO{Status: "NO_FUTURE_OCCURRENCE", Reason: "operation produces no occurrence after now"},
	})
	if err != nil {
		t.Fatalf("EncodeChangePreview() error = %v", err)
	}
	if !strings.Contains(string(data), `"status":"NO_FUTURE_OCCURRENCE"`) {
		t.Fatalf("encoded = %s", data)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	conflict := decoded["conflict"].(map[string]any)
	if _, present := conflict["target"]; present {
		t.Fatalf("unchecked conflict contains a target: %s", data)
	}
	if _, present := conflict["batch_unix"]; present {
		t.Fatalf("unchecked conflict contains batch time: %s", data)
	}
}

// TestEncodeErrorRejectsNilCause 验证错误编码边界不会吞掉 nil 错误。
func TestEncodeErrorRejectsNilCause(t *testing.T) {
	if _, err := EncodeError(nil); err == nil {
		t.Fatal("EncodeError(nil) error = nil")
	}
}
