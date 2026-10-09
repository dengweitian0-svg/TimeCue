package handler

import (
	"log/slog"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// StartDeliveryHandler 开始前冻结合法成员；空组返回无需展示，重复请求幂等。
func StartDeliveryHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		var request struct{}
		if failure := readDeliveryJSON(c, &request); failure != nil {
			return *failure
		}
		value, err := app.StartDelivery(c.Request.Context(), c.Param("id"))
		if err != nil {
			return deliveryError(err)
		}
		result := middleware.DataResult(http.StatusOK, value)
		result.LogFields = append(deliveryFields(value.DeliveryTaskID, value.AttemptID, value.MemberCount), slog.Int("attempt_number", value.AttemptNumber), slog.Bool("should_display", value.ShouldDisplay))
		return result
	}
}
