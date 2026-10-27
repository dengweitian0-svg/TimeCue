package chat

import (
	"context"
	"encoding/json"
)

// EstimateTokens 统一以 UTF-8 字节数除以四并向上取整估算 token，不作为实际 usage。
func EstimateTokens(encoded []byte) int {
	return (len(encoded) + 3) / 4
}

func (s *Runtime) newRequest(ctx context.Context, r Record, cfg ModelConfig) (ModelRequest, error) {
	history, err := s.execution.History(ctx, r.SessionID)
	if err != nil {
		return ModelRequest{}, err
	}
	return ModelRequest{
		Model: r.Model,
		Messages: append(history, Message{
			Role:    "user",
			Content: r.Input,
		}),
		Stream:        true,
		MaxTokens:     cfg.MaxOutputTokens,
		StreamOptions: map[string]bool{"include_usage": true},
	}, nil
}

func requestBudgetError(input ModelRequest, cfg ModelConfig) *RunError {
	encoded, _ := json.Marshal(input)
	if EstimateTokens(encoded) > cfg.ContextWindow-cfg.MaxOutputTokens {
		return &RunError{
			Code:    "context_budget_exceeded",
			Message: "输入超出模型上下文预算",
		}
	}
	return nil
}
