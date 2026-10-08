package handler

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// AcknowledgementResponse 保存实际返回的确认事实，供统一日志提取结果数量。
type AcknowledgementResponse struct {
	NotificationID int        `json:"notificationId"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt"`
}

// AcknowledgementsHandler 统一确认一个或多个明确 ID，不接受动态的全部未确认选择条件。
func AcknowledgementsHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			return middleware.ErrorResult(http.StatusUnsupportedMediaType, errUnsupportedMediaType, nil)
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return middleware.ErrorResult(http.StatusRequestEntityTooLarge, errPayloadTooLarge, nil)
			}
			return middleware.ErrorResult(http.StatusBadRequest, errInvalidJSON, nil)
		}
		var request struct {
			NotificationIDs []int `json:"notificationIds"`
		}
		if requireJSONKeys(body, "notificationIds") != nil || decodeStrict(bytes.NewReader(body), &request) != nil {
			return middleware.ErrorResult(http.StatusBadRequest, errInvalidJSON, nil)
		}
		values, err := app.AcknowledgeNotifications(c.Request.Context(), request.NotificationIDs, time.Now())
		if err != nil {
			return resourceError(err)
		}
		result := make([]AcknowledgementResponse, 0, len(values))
		for _, value := range values {
			result = append(result, AcknowledgementResponse{
				NotificationID: value.ID,
				AcknowledgedAt: value.AcknowledgedAt,
			})
		}
		return middleware.DataResult(http.StatusOK, result)
	}
}
