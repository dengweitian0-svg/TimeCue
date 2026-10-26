package api

import (
	"log/slog"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/agent"
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/session"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

func registerChatRoutes(router *gin.Engine, services chat.Services) {
	v1 := router.Group("/v1")
	v1.GET("/model", middleware.WrapHTTPHandler(agent.GetModelsHandler(services.Model), nil))
	v1.GET("/run/:run_id/events", middleware.WrapHTTPHandler(agent.GetRunEventsHandler(services.Run), nil))
	v1.POST("/run", middleware.WrapHTTPHandler(agent.CreateRunHandler(services.Run), createRunFields))
	v1.GET("/run/:run_id", middleware.WrapHTTPHandler(agent.GetRunHandler(services.Run), getRunFields))
	v1.GET("/run/:run_id/event", middleware.WrapHTTPHandler(agent.RunEventsHandler(services.Run), runEventsFields))
	v1.POST("/run/:run_id/cancel", middleware.WrapHTTPHandler(agent.CancelRunHandler(services.Run), cancelRunFields))
	v1.GET("/session/:session_id", middleware.WrapHTTPHandler(session.GetSessionHandler(services.Session), getSessionFields))
	v1.GET("/session", middleware.WrapHTTPHandler(session.ListSessionsHandler(services.Session), listSessionsFields))
	v1.DELETE("/session/:session_id", middleware.WrapHTTPHandler(session.DeleteSessionHandler(services.Session), deleteSessionFields))
}

func createRunFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusAccepted {
		return nil
	}
	response := result.Body.(agent.CreateRunResponse)
	return []slog.Attr{slog.String("run_id", response.RunID)}
}

func getRunFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(agent.RunResponse)
	return []slog.Attr{slog.String("run_id", response.RunID), slog.String("run_status", string(response.Status))}
}

func runEventsFields(c *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	return []slog.Attr{slog.String("run_id", c.Param("run_id"))}
}

func cancelRunFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK && result.Status != http.StatusAccepted {
		return nil
	}
	response := result.Body.(agent.CancelRunResponse)
	return []slog.Attr{slog.String("run_id", response.RunID), slog.String("run_status", string(response.Status))}
}

func getSessionFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(session.SessionResponse)
	return []slog.Attr{slog.String("session_id", response.SessionID), slog.Int("count", len(response.RunIDs))}
}

func listSessionsFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(session.SessionsResponse)
	return []slog.Attr{slog.Int("count", len(response.Sessions))}
}

func deleteSessionFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(session.DeleteSessionResponse)
	return []slog.Attr{slog.String("session_id", response.SessionID)}
}
