package chat

import (
	"context"
)

func hasModelOutput(output ModelOutput) bool {
	return output.Message.Content != "" || output.Message.ReasoningContent != "" || len(output.Message.ToolCalls) > 0
}

func (s *Runtime) advance(ctx context.Context, r *Record, cfg ModelConfig, call ModelCall) (ModelCall, *RunError) {
	for r.Error == nil {
		if call.State == StateSucceeded && (call.CheckpointStage == "model_call_end" || call.CheckpointStage == "tool_start") {
			var failure *RunError
			call, failure = s.processTools(ctx, call, *r, cfg)
			if failure != nil || ctx.Err() != nil {
				return call, failure
			}
		}
		if ctx.Err() != nil {
			return call, nil
		}
		if call.State == StateSucceeded && call.CheckpointStage == "tool_end" {
			var ok bool
			cfg, ok = s.currentModel(r.Provider, r.Model)
			if !ok {
				return call, toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
			}
			request := call.Input
			request.Messages = call.CheckpointContext
			request.MaxTokens = cfg.MaxOutputTokens
			call = ModelCall{
				RunID:     r.RunID,
				SessionID: r.SessionID,
				Input:     request,
				State:     StateRunning,
				MaxRetry:  cfg.MaxRetry,
			}
		}
		if call.ID == 0 {
			var err error
			if call.Input.Model == "" {
				call.Input, err = s.newRequest(ctx, *r, cfg)
			}
			if err != nil {
				return call, persistenceFailure()
			}
			if failure := requestBudgetError(call.Input, cfg); failure != nil {
				return ModelCall{
					RunID:     r.RunID,
					SessionID: r.SessionID,
				}, failure
			}
			count, err := s.execution.OrdinaryCallCount(ctx, r.RunID)
			if err != nil {
				return call, persistenceFailure()
			}
			limit := cfg.MaxRounds
			if limit <= 0 {
				limit = 20
			}
			if count >= limit {
				return ModelCall{
					RunID:     r.RunID,
					SessionID: r.SessionID,
				}, &RunError{
					Code:    "model_round_limit_exceeded",
					Message: "已达到普通模型轮数上限，任务仍需继续",
				}
			}
			call.ID, err = s.execution.BeginCall(ctx, call)
			if err != nil {
				if ctx.Err() != nil {
					return call, nil
				}
				return call, persistenceFailure()
			}
			call = call.WithCallStarted()
		}
		var failure *RunError
		call, failure = s.complete(ctx, *r, call)
		r.Output = nil
		if call.Output != nil && call.Output.Message.Content != "" {
			content := call.Output.Message.Content
			r.Output = &content
		}
		if failure != nil || ctx.Err() != nil {
			return call, failure
		}
		if call.Output == nil || len(call.Output.Message.ToolCalls) == 0 {
			return call, nil
		}
		if s.tools == nil || s.toolStore == nil {
			return call, &RunError{
				Code:    "model_invalid_format",
				Message: "工具执行端口未装配",
			}
		}
		accepted := call.WithResponseAccepted()
		if err := s.toolStore.AcceptResponse(context.Background(), accepted); err != nil {
			return call, persistenceFailure()
		}
		call = accepted
		call.PendingEvents = nil
	}
	return call, r.Error
}

func toolError(code, message string) *RunError {
	return &RunError{
		Code:    code,
		Message: message,
	}
}
