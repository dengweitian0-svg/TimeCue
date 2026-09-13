package app

import (
	"bytes"
	"errors"

	json "encoding/json/v2"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// DecodeCreateSchedule 使用 encoding/json/v2 解码创建请求。
func DecodeCreateSchedule(data []byte) (CreateScheduleRequest, error) {
	var request CreateScheduleRequest
	if err := json.Unmarshal(data, &request); err != nil {
		// JSON 格式错误在边界层转换为业务错误，领域层只接收已类型化输入。
		return CreateScheduleRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "invalid create schedule JSON",
			Cause:   err,
		}
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return CreateScheduleRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "create schedule JSON must be an object",
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return CreateScheduleRequest{}, &domain.Error{Code: domain.Validation, Message: "create schedule JSON must be an object", Cause: err}
	}
	_, request.CronPresent = fields["cron"]
	_, request.UntilUnixPresent = fields["until_unix"]
	return request, nil
}

// DecodeUpdateSchedule 使用 encoding/json/v2 解码更新补丁并记录字段存在性。
func DecodeUpdateSchedule(data []byte) (UpdateScheduleRequest, error) {
	var request UpdateScheduleRequest
	if err := json.Unmarshal(data, &request); err != nil {
		// JSON 格式错误在边界层转换为业务错误，领域层只接收已类型化输入。
		return UpdateScheduleRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "invalid update schedule JSON",
			Cause:   err,
		}
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return UpdateScheduleRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "update schedule JSON must be an object",
		}
	}

	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return UpdateScheduleRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "update schedule JSON must be an object",
			Cause:   err,
		}
	}
	_, request.TitlePresent = fields["title"]
	_, request.NotesPresent = fields["notes"]
	_, request.StartUnixPresent = fields["start_unix"]
	_, request.EndUnixPresent = fields["end_unix"]
	_, request.CronPresent = fields["cron"]
	_, request.CancelPresent = fields["cancel"]
	_, request.UntilUnixPresent = fields["until_unix"]
	_, request.RestorePresent = fields["restore"]
	_, request.ExpectedRevisionPresent = fields["expected_revision"]
	return request, nil
}

// DecodeUpdateOccurrence 使用 encoding/json/v2 解码例外补丁并记录字段存在性。
func DecodeUpdateOccurrence(data []byte) (UpdateOccurrenceRequest, error) {
	var request UpdateOccurrenceRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return UpdateOccurrenceRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "invalid update occurrence JSON",
			Cause:   err,
		}
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return UpdateOccurrenceRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "update occurrence JSON must be an object",
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return UpdateOccurrenceRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "update occurrence JSON must be an object",
			Cause:   err,
		}
	}
	_, request.OriginalDatePresent = fields["original_date"]
	_, request.StartUnixPresent = fields["start_unix"]
	_, request.EndUnixPresent = fields["end_unix"]
	_, request.TitlePresent = fields["title"]
	_, request.NotesPresent = fields["notes"]
	_, request.ExpectedRevisionPresent = fields["expected_revision"]
	return request, nil
}

// DecodeOccurrenceAction 使用 encoding/json/v2 解码取消或恢复请求。
func DecodeOccurrenceAction(data []byte) (OccurrenceActionRequest, error) {
	var request OccurrenceActionRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return OccurrenceActionRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "invalid occurrence action JSON",
			Cause:   err,
		}
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return OccurrenceActionRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "occurrence action JSON must be an object",
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return OccurrenceActionRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "occurrence action JSON must be an object",
			Cause:   err,
		}
	}
	_, request.OriginalDatePresent = fields["original_date"]
	_, request.ExpectedRevisionPresent = fields["expected_revision"]
	return request, nil
}

// DecodeDeleteSchedule 使用 encoding/json/v2 解码永久删除请求。
func DecodeDeleteSchedule(data []byte) (DeleteScheduleRequest, error) {
	var request DeleteScheduleRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return DeleteScheduleRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "invalid delete schedule JSON",
			Cause:   err,
		}
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return DeleteScheduleRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "delete schedule JSON must be an object",
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return DeleteScheduleRequest{}, &domain.Error{
			Code:    domain.Validation,
			Message: "delete schedule JSON must be an object",
			Cause:   err,
		}
	}
	_, request.ExpectedRevisionPresent = fields["expected_revision"]
	return request, nil
}

// EncodeSchedule 使用 encoding/json/v2 序列化单条日程。
func EncodeSchedule(schedule ScheduleDTO) ([]byte, error) {
	return json.Marshal(schedule)
}

// EncodeOccurrence 使用 encoding/json/v2 序列化一次发生。
func EncodeOccurrence(occurrence OccurrenceDTO) ([]byte, error) {
	return json.Marshal(occurrence)
}

// EncodeOccurrenceAction 使用 encoding/json/v2 序列化取消或恢复结果。
func EncodeOccurrenceAction(result OccurrenceActionDTO) ([]byte, error) {
	return json.Marshal(result)
}

// EncodeListSchedules 使用 encoding/json/v2 序列化列表结果。
func EncodeListSchedules(result ListSchedulesResponse) ([]byte, error) {
	return json.Marshal(result)
}

// EncodeTimeBounds 使用 encoding/json/v2 序列化时间边界。
func EncodeTimeBounds(bounds TimeBoundsDTO) ([]byte, error) {
	return json.Marshal(bounds)
}

// EncodeError 使用 encoding/json/v2 序列化业务错误。
func EncodeError(err error) ([]byte, error) {
	if err == nil {
		return nil, errors.New("cannot encode a nil error")
	}

	response := errorDTO{Code: "INTERNAL", Message: "internal error"}
	var businessErr *domain.Error
	if errors.As(err, &businessErr) {
		response.Code = string(businessErr.Code)
		response.Message = businessErr.Message
		response.Fields = append([]string(nil), businessErr.Fields...)
		if businessErr.Bounds != nil {
			bounds := timeBoundsDTO(*businessErr.Bounds)
			response.Bounds = &bounds
		}
	}
	return json.Marshal(response)
}

type errorDTO struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Fields  []string       `json:"fields,omitzero"`
	Bounds  *TimeBoundsDTO `json:"bounds,omitzero"`
}

// timeBoundsDTO 将领域时间边界投影为 Unix 秒，避免向 JSON 暴露时区对象。
func timeBoundsDTO(bounds domain.TimeBounds) TimeBoundsDTO {
	return TimeBoundsDTO{
		SingleMinUnix:          bounds.SingleMin.Unix(),
		SingleMaxExclusiveUnix: bounds.SingleMaxExclusive.Unix(),
		QueryMaxExclusiveUnix:  bounds.QueryMaxExclusive.Unix(),
	}
}
