package schedule

import (
	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
)

// UpdateScheduleHandler 以 expectedRevision 防止草稿覆盖并发变更。
func UpdateScheduleHandler(app *application.App) middleware.HTTPHandler {
	return writeScheduleHandler(app, true)
}
