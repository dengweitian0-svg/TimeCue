package chat

import (
	"context"
	"errors"
)

func hasModelOutput(output ModelOutput) bool {
	return output.Message.Content != "" || output.Message.ReasoningContent != "" || len(output.Message.ToolCalls) > 0
}

func (s *Runtime) advance(ctx context.Context, r *Record, cfg ModelConfig, call ModelCall) (ModelCall, *RunError) {
	for r.Error == nil {
		hasTools := call.Output != nil && len(call.Output.Message.ToolCalls) > 0
		if call.State == StateSucceeded && hasTools && (call.CheckpointStage == "model_call_end" || call.CheckpointStage == "tool_start") {
			var failure *RunError
			call, failure = s.processTools(ctx, call, *r, cfg)
			if failure != nil || ctx.Err() != nil {
				return call, failure
			}
		}
		if ctx.Err() != nil {
			return call, nil
		}
		if call.State == StateSucceeded && (call.CheckpointStage == "tool_end" || !hasTools) {
			continuation := call.Output != nil && call.Output.FinishReason == "length"
			if !hasTools && !continuation && call.CheckpointStage != "tool_end" {
				return call, nil
			}
			var ok bool
			cfg, ok = s.currentModel(r.Provider, r.Model)
			if !ok {
				return call, toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
			}
			if continuation {
				count, err := s.execution.ContinuationCallCount(ctx, r.RunID)
				if err != nil {
					if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
						return call, nil
					}
					return call, persistenceFailure()
				}
				limit := cfg.MaxContinue
				if limit <= 0 {
					limit = 5
				}
				if count >= limit {
					return call, toolError("model_continue_limit_exceeded", "已达到自动续写上限，输出仍未完成")
				}
			}
			request := call.FollowupRequest(continuation)
			request.MaxTokens = cfg.MaxOutputTokens
			prefix := ""
			if continuation && r.Output != nil {
				prefix = *r.Output
			}
			call = ModelCall{
				RunID:        r.RunID,
				SessionID:    r.SessionID,
				Input:        request,
				State:        StateRunning,
				MaxRetry:     cfg.MaxRetry,
				Continuation: continuation,
				OutputPrefix: prefix,
			}
		}
		if call.ID == 0 {
			var err error
			if call.Input.Model == "" {
				call.Input, err = s.newRequest(ctx, *r, cfg)
			}
			if err != nil {
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
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
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
				return call, persistenceFailure()
			}
			limit := cfg.MaxRounds
			if limit <= 0 {
				limit = 20
			}
			if !call.Continuation && count >= limit {
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
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
				return call, persistenceFailure()
			}
			call = call.WithCallStarted()
		}
		var failure *RunError
		call, failure = s.complete(ctx, *r, call)
		r.Output = call.PresentedOutput()
		if failure != nil || ctx.Err() != nil {
			return call, failure
		}
		if call.Output == nil || len(call.Output.Message.ToolCalls) == 0 && call.Output.FinishReason != "length" {
			return call, nil
		}
		if s.toolStore == nil || len(call.Output.Message.ToolCalls) > 0 && s.tools == nil {
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
