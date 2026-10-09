package handler

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// readDeliveryJSON 沿用正式写入接口的大小、媒体类型及严格字段限制。
func readDeliveryJSON(c *gin.Context, target any, keys ...string) *middleware.HTTPResult {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		r := middleware.ErrorResult(http.StatusUnsupportedMediaType, errUnsupportedMediaType, nil)
		return &r
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		var maxErr *http.MaxBytesError
		status, apiErr := http.StatusBadRequest, errInvalidJSON
		if errors.As(err, &maxErr) {
			status, apiErr = http.StatusRequestEntityTooLarge, errPayloadTooLarge
		}
		r := middleware.ErrorResult(status, apiErr, nil)
		return &r
	}
	if requireJSONKeys(body, keys...) != nil || decodeStrict(bytes.NewReader(body), target) != nil {
		r := middleware.ErrorResult(http.StatusBadRequest, errInvalidJSON, nil)
		return &r
	}
	return nil
}

func deliveryError(err error) middleware.HTTPResult {
	switch {
	case errors.Is(err, service.ErrDeliveryInput):
		return middleware.ErrorResult(http.StatusBadRequest, newAPIError("invalid_request", "invalid delivery input"), nil)
	case errors.Is(err, service.ErrAttemptNotFound):
		return middleware.ErrorResult(http.StatusNotFound, newAPIError("delivery_attempt_not_found", "attempt is not valid in this process"), nil)
	case errors.Is(err, service.ErrDeliveryConflict):
		return middleware.ErrorResult(http.StatusConflict, newAPIError("delivery_conflict", "attempt conflicts with accepted state"), nil)
	case errors.Is(err, service.ErrDeliveryStopping):
		return middleware.ErrorResult(http.StatusServiceUnavailable, newAPIError("delivery_stopping", "delivery is stopping"), nil)
	default:
		return internalErrorResult(err)
	}
}

// deliveryFields 只记录身份和数量，提交事实在响应写入失败时仍保留。
func deliveryFields(taskID, attemptID string, count int) []slog.Attr {
	return []slog.Attr{slog.String("delivery_task_id", taskID), slog.String("attempt_id", attemptID), slog.Int("count", count)}
}
