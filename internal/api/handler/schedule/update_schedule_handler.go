package schedule

import (
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
)

// UpdateScheduleHandler 以 expectedRevision 防止草稿覆盖并发变更。
func UpdateScheduleHandler(app Service) middleware.HTTPHandler {
	return writeScheduleHandler(app, true)
}
