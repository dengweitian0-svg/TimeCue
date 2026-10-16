package schedule

import (
	service "github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
	"log/slog"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// withScheduleLog 附加受控业务摘要，不写日志、不修改对外响应。
func withScheduleLog(result middleware.HTTPResult, write *model.ScheduleWrite) middleware.HTTPResult {
	result.LogFields = []slog.Attr{
		slog.Int("schedule_id", write.Schedule.ID),
		slog.Any("type", write.Schedule.Type),
		slog.Any("mutation", service.SummarizeScheduleWrite(write)),
	}
	if write.Replayed {
		result.SuccessMessage = "schedule write replayed"
	}
	return result
}
