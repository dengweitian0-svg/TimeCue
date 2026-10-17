package schedule

import (
	"encoding/json"
	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	service "github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"log/slog"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// ClaimDeliveryHandler 领取完整任务；省略 limit 默认 20，显式 null 或越界拒绝。
func ClaimDeliveryHandler(app *application.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		var request struct {
			Limit json.RawMessage `json:"limit"`
		}
		if failure := readDeliveryJSON(c, &request, "limit"); failure != nil {
			return *failure
		}
		limit := 20
		if len(request.Limit) > 0 {
			if string(request.Limit) == "null" || json.Unmarshal(request.Limit, &limit) != nil {
				return deliveryError(service.ErrDeliveryInput)
			}
		}
		values, err := app.ClaimDelivery(c.Request.Context(), limit)
		if err != nil {
			return deliveryError(err)
		}
		result := middleware.DataResult(http.StatusOK, values)
		result.LogFields = []slog.Attr{slog.Int("count", len(values))}
		return result
	}
}
