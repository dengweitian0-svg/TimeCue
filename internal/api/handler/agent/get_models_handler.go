package agent

import (
	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
)

// GetModelsHandler 仅返回本地模型配置及当前可用性。
func GetModelsHandler(service chat.ModelService) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		models, err := service.GetModels(c.Request.Context())
		if err != nil {
			return utils.ChatError(err)
		}
		return middleware.JSONResult(http.StatusOK, struct {
			Models []chat.ModelOption `json:"models"`
		}{models})
	}
}
