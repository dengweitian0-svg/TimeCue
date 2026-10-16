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

// DeliveryExecutorExitHandler 接受与当前 Go 运行身份匹配的明确执行者退出事件。
func DeliveryExecutorExitHandler(app *application.App, info application.RuntimeInfo) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		var request struct {
			InstanceID string          `json:"instanceId"`
			AttemptIDs json.RawMessage `json:"attemptIds"`
		}
		if failure := readDeliveryJSON(c, &request, "instanceId", "attemptIds"); failure != nil {
			return *failure
		}
		if request.InstanceID == "" {
			return deliveryError(service.ErrAttemptNotFound)
		}
		if request.InstanceID != info.InstanceID {
			return deliveryError(service.ErrAttemptNotFound)
		}
		var attemptIDs []string
		if len(request.AttemptIDs) == 0 || string(request.AttemptIDs) == "null" || json.Unmarshal(request.AttemptIDs, &attemptIDs) != nil {
			return deliveryError(service.ErrDeliveryInput)
		}
		for _, id := range attemptIDs {
			if id == "" {
				return deliveryError(service.ErrDeliveryInput)
			}
		}
		app.DeliveryExecutorExitedAttempts(attemptIDs)
		result := middleware.DataResult(http.StatusOK, struct {
			Accepted bool `json:"accepted"`
		}{Accepted: true})
		result.LogFields = []slog.Attr{slog.String("instance_id", request.InstanceID), slog.Int("attempt_count", len(attemptIDs))}
		return result
	}
}
