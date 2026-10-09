package handler

import (
	"log/slog"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// DeliveryResultsHandler 回填一次分组平台请求的结果，成员仅取服务端冻结集合。
func DeliveryResultsHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		var request struct {
			Outcome model.NotifyStatus `json:"outcome"`
		}
		if failure := readDeliveryJSON(c, &request, "outcome"); failure != nil {
			return *failure
		}
		value, err := app.CompleteDelivery(c.Request.Context(), c.Param("id"), request.Outcome)
		if err != nil {
			return deliveryError(err)
		}
		result := middleware.DataResult(http.StatusOK, value)
		result.LogFields = []slog.Attr{slog.String("attempt_id", value.AttemptID), slog.String("outcome", string(value.Outcome)), slog.Int("count", value.MemberCount)}
		return result
	}
}
