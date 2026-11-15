package loop

import (
	"encoding/json"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

func requestBudgetError(body string, cfg chat.ModelConfig) *chat.RunError {
	if chat.EstimateTokens([]byte(body)) > cfg.ContextWindow-cfg.MaxOutputTokens {
		return &chat.RunError{
			Code:    "context_budget_exceeded",
			Message: "输入超出模型上下文预算",
		}
	}
	return nil
}

func (s *Run) prepareRequest(provider string, input chat.ModelRequest) (chat.PreparedRequest, *chat.RunError) {
	body, failure := s.model.PrepareRequest(provider, input)
	if failure != nil {
		return chat.PreparedRequest{}, failure
	}
	prepared := chat.PreparedRequest{Body: body}
	if err := json.Unmarshal([]byte(body), &prepared.Input); err != nil || prepared.Input.Model != input.Model {
		return chat.PreparedRequest{}, toolError("model_invalid_params", "模型请求参数无效")
	}
	return prepared, nil
}

func (s *Run) prepareInitialCall(r chat.Record, input chat.ModelRequest, cfg chat.ModelConfig) (chat.ModelCall, *chat.RunError) {
	call := chat.ModelCall{
		RunID:     r.RunID,
		SessionID: r.SessionID,
		State:     chat.StateRunning,
		MaxRetry:  cfg.MaxRetry,
	}
	prepared, failure := s.prepareRequest(r.Provider, input)
	if failure != nil {
		return call, failure
	}
	if summary, ok := s.compactor.BeforeFirstCall(prepared, cfg); ok {
		call.Input = summary
		call.CheckpointStage = "compact"
	} else {
		call.Input, call.InputBody = prepared.Input, prepared.Body
	}
	return call, nil
}
