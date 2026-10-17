package schedule

import (
	"errors"
	utils "github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	notification "github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
)

// readDeliveryJSON 校验投递写入请求的大小、媒体类型和字段。
func readDeliveryJSON(c *gin.Context, target any, keys ...string) *middleware.HTTPResult {
	return utils.ReadJSON(c, target, keys...)
}

func deliveryError(err error) middleware.HTTPResult {
	switch {
	case errors.Is(err, notification.ErrDeliveryInput):
		return middleware.ErrorResult(http.StatusBadRequest, utils.NewAPIError("invalid_request", "invalid delivery input"), nil)
	case errors.Is(err, notification.ErrAttemptNotFound):
		return middleware.ErrorResult(http.StatusNotFound, utils.NewAPIError("delivery_attempt_not_found", "attempt is not valid in this process"), nil)
	case errors.Is(err, notification.ErrDeliveryConflict):
		return middleware.ErrorResult(http.StatusConflict, utils.NewAPIError("delivery_conflict", "attempt conflicts with accepted state"), nil)
	case errors.Is(err, notification.ErrDeliveryStopping):
		return middleware.ErrorResult(http.StatusServiceUnavailable, utils.NewAPIError("delivery_stopping", "delivery is stopping"), nil)
	default:
		return utils.InternalErrorResult(err)
	}
}

// deliveryFields 只记录身份和数量，提交事实在响应写入失败时仍保留。
func deliveryFields(taskID, attemptID string, count int) []slog.Attr {
	return []slog.Attr{slog.String("delivery_task_id", taskID), slog.String("attempt_id", attemptID), slog.Int("count", count)}
}
