package api

import (
	"log/slog"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

func registerChatRoutes(router *gin.Engine, service chat.Service) {
	v1 := router.Group("/v1")
	v1.POST("/run", middleware.WrapHTTPHandler(handler.CreateRunHandler(service), createRunFields))
	v1.GET("/run/:run_id", middleware.WrapHTTPHandler(handler.GetRunHandler(service), getRunFields))
	v1.GET("/run/:run_id/event", middleware.WrapHTTPHandler(handler.RunEventsHandler(service), runEventsFields))
	v1.POST("/run/:run_id/cancel", middleware.WrapHTTPHandler(handler.CancelRunHandler(service), cancelRunFields))
	v1.GET("/session/:session_id", middleware.WrapHTTPHandler(handler.GetSessionHandler(service), getSessionFields))
	v1.GET("/session", middleware.WrapHTTPHandler(handler.ListSessionsHandler(service), listSessionsFields))
	v1.DELETE("/session/:session_id", middleware.WrapHTTPHandler(handler.DeleteSessionHandler(service), deleteSessionFields))
}

func createRunFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusAccepted {
		return nil
	}
	response := result.Body.(handler.CreateRunResponse)
	return []slog.Attr{slog.String("run_id", response.RunID)}
}

func getRunFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(handler.RunResponse)
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
	response := result.Body.(handler.CancelRunResponse)
	return []slog.Attr{slog.String("run_id", response.RunID), slog.String("run_status", string(response.Status))}
}

func getSessionFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(handler.SessionResponse)
	return []slog.Attr{slog.String("session_id", response.SessionID), slog.Int("count", len(response.RunIDs))}
}

func listSessionsFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(handler.SessionsResponse)
	return []slog.Attr{slog.Int("count", len(response.Sessions))}
}

func deleteSessionFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(handler.DeleteSessionResponse)
	return []slog.Attr{slog.String("session_id", response.SessionID)}
}
