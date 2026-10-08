package handler

import (
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
)

// UpdateScheduleHandler 以 expectedRevision 防止草稿覆盖并发变更。
func UpdateScheduleHandler(app *service.App) middleware.HTTPHandler {
	return writeScheduleHandler(app, true)
}
