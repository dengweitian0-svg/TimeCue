package chat

import (
	"context"
	"errors"
)

func (s *Runtime) advance(ctx context.Context, r *Record, cfg ModelConfig, call ModelCall) (ModelCall, *RunError) {
	for r.Error == nil {
		// 无 checkpoint 的恢复仍属于首次调用前，沿同一创建前阈值检查。
		if call.ID == 0 && call.Input.Model == "" {
			var ok bool
			cfg, ok = s.currentModel(r.Provider, r.Model)
			if !ok {
				return call, toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
			}
			request, err := s.contextBuilder.firstRequest(ctx, *r, cfg)
			if err != nil {
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
				return call, persistenceFailure(call, "history", err)
			}
			prepared, failure := s.prepareRequest(r.Provider, request)
			if failure != nil {
				return call, failure
			}
			call.Input = prepared.Input
			call.InputBody = prepared.Body
			if needsCompact(prepared.Input, prepared.Body, cfg) {
				call.Input = compactRequest(request)
				call.InputBody = ""
				call.CheckpointStage = "compact"
			}
		}
		if call.CheckpointStage == "compact" {
			var failure *RunError
			call, failure = s.advanceCompact(ctx, r, cfg, call)
			if failure != nil || ctx.Err() != nil || r.State == StateWaitForCancel || call.CheckpointStage == "compact" {
				return call, failure
			}
		}
		hasTools := call.hasToolCalls()
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
				if failure := s.continuationBudgetError(ctx, call, cfg); failure != nil || ctx.Err() != nil {
					return call, failure
				}
			}
			request := s.contextBuilder.followupRequest(call, continuation)
			request.MaxTokens = cfg.MaxOutputTokens
			prepared, failure := s.prepareRequest(r.Provider, request)
			if failure != nil {
				return call, failure
			}
			if call.CheckpointStage == "tool_end" && requestBudgetError(prepared.Body, cfg) != nil {
				base, err := s.contextBuilder.firstRequest(ctx, *r, cfg)
				if err != nil {
					if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
						return call, nil
					}
					return call, persistenceFailure(call, "compact_history", err)
				}
				prefix := ""
				if r.Output != nil {
					prefix = *r.Output
				}
				call = ModelCall{
					RunID:             r.RunID,
					SessionID:         r.SessionID,
					Input:             runtimeCompactRequest(request, base),
					State:             StateRunning,
					MaxRetry:          cfg.MaxRetry,
					CheckpointStage:   "compact",
					CheckpointContext: request.Messages,
					OutputPrefix:      prefix,
				}
				continue
			}
			prefix := ""
			if continuation && r.Output != nil {
				prefix = *r.Output
			}
			call = ModelCall{
				RunID:        r.RunID,
				SessionID:    r.SessionID,
				Input:        prepared.Input,
				InputBody:    prepared.Body,
				State:        StateRunning,
				MaxRetry:     cfg.MaxRetry,
				Continuation: continuation,
				OutputPrefix: prefix,
			}
		}
		if call.ID == 0 {
			var err error
			if failure := requestBudgetError(call.InputBody, cfg); failure != nil {
				return ModelCall{
					RunID:     r.RunID,
					SessionID: r.SessionID,
				}, failure
			}
			if !call.Continuation {
				if failure := s.ordinaryRoundBudgetError(ctx, call, cfg); failure != nil || ctx.Err() != nil {
					return call, failure
				}
			}
			call.ID, err = s.execution.BeginCall(ctx, call)
			if err != nil {
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
				return call, persistenceFailure(call, "model_start", err)
			}
			call = call.WithCallStarted()
		}
		var failure *RunError
		call, failure = s.complete(ctx, *r, call)
		r.Output = call.PresentedOutput()
		if failure != nil || ctx.Err() != nil {
			return call, failure
		}
		if call.Output == nil || !call.hasToolCalls() && call.Output.FinishReason != "length" {
			return call, nil
		}
		if s.toolStore == nil || call.hasToolCalls() && s.tools == nil {
			return call, &RunError{
				Code:    "model_invalid_format",
				Message: "工具执行端口未装配",
			}
		}
		accepted := call.WithResponseAccepted()
		if err := s.toolStore.AcceptResponse(s.persistenceContext(), accepted); err != nil {
			return call, persistenceFailure(call, "model_response", err)
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

func (s *Runtime) ordinaryRoundBudgetError(ctx context.Context, call ModelCall, cfg ModelConfig) *RunError {
	count, err := s.execution.OrdinaryCallCount(ctx, call.RunID)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil
		}
		return persistenceFailure(call, "model_round_count", err)
	}
	limit := cfg.MaxRounds
	if limit <= 0 {
		limit = 20
	}
	if count >= limit {
		return toolError("model_round_limit_exceeded", "已达到普通模型轮数上限，任务仍需继续")
	}
	return nil
}

func (s *Runtime) continuationBudgetError(ctx context.Context, call ModelCall, cfg ModelConfig) *RunError {
	count, err := s.execution.ContinuationCallCount(ctx, call.RunID)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil
		}
		return persistenceFailure(call, "continuation_count", err)
	}
	limit := cfg.MaxContinue
	if limit <= 0 {
		limit = 5
	}
	if count >= limit {
		return toolError("model_continue_limit_exceeded", "已达到自动续写上限，输出仍未完成")
	}
	return nil
}
