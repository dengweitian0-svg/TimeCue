package chat

import "encoding/json"

// EstimateTokens 统一以 UTF-8 字节数除以四并向上取整估算 token，不作为实际 usage。
func EstimateTokens(encoded []byte) int {
	return (len(encoded) + 3) / 4
}

func requestBudgetError(body string, cfg ModelConfig) *RunError {
	if EstimateTokens([]byte(body)) > cfg.ContextWindow-cfg.MaxOutputTokens {
		return &RunError{
			Code:    "context_budget_exceeded",
			Message: "输入超出模型上下文预算",
		}
	}
	return nil
}

func (s *Runtime) prepareRequest(provider string, input ModelRequest) (PreparedRequest, *RunError) {
	body, failure := s.model.PrepareRequest(provider, input)
	if failure != nil {
		return PreparedRequest{}, failure
	}
	prepared := PreparedRequest{Body: body}
	if err := json.Unmarshal([]byte(body), &prepared.Input); err != nil || prepared.Input.Model != input.Model {
		return PreparedRequest{}, toolError("model_invalid_params", "模型请求参数无效")
	}
	return prepared, nil
}

func (s *Runtime) prepareInitialCall(r Record, input ModelRequest, cfg ModelConfig) (ModelCall, *RunError) {
	call := ModelCall{
		RunID:     r.RunID,
		SessionID: r.SessionID,
		State:     StateRunning,
		MaxRetry:  cfg.MaxRetry,
	}
	prepared, failure := s.prepareRequest(r.Provider, input)
	if failure != nil {
		return call, failure
	}
	if summary, ok := s.compactor.beforeFirstCall(prepared, cfg); ok {
		call.Input = summary
		call.CheckpointStage = "compact"
	} else {
		call.Input, call.InputBody = prepared.Input, prepared.Body
	}
	return call, nil
}
