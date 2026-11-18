package loop

import (
	"context"
	"errors"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

func (s *Run) advance(ctx context.Context, r *chat.Record, cfg chat.ModelConfig, call chat.ModelCall) (chat.ModelCall, *chat.RunError) {
	for r.Error == nil {
		// 无 checkpoint 的恢复仍属于首次调用前，沿同一创建前阈值检查。
		if call.ID == 0 && call.Input.Model == "" {
			var ok bool
			cfg, ok = s.currentModel(r.Provider, r.Model)
			if !ok {
				return call, toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
			}
			request, err := s.contextBuilder.FirstRequest(ctx, *r, cfg)
			if err != nil {
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
				return call, persistenceFailure(call, "history", err)
			}
			var failure *chat.RunError
			call, failure = s.prepareInitialCall(*r, request, cfg)
			if failure != nil {
				return call, failure
			}
		}
		if call.CheckpointStage == "compact" {
			var failure *chat.RunError
			call, failure = s.advanceCompact(ctx, r, cfg, call)
			if failure != nil || ctx.Err() != nil || r.State == chat.StateWaitForCancel || call.CheckpointStage == "compact" {
				return call, failure
			}
		}
		hasTools := call.HasToolCalls()
		if call.State == chat.StateSucceeded && hasTools && (call.CheckpointStage == "model_call_end" || call.CheckpointStage == "tool_start") {
			var failure *chat.RunError
			call, failure = s.processTools(ctx, call, *r, cfg)
			if failure != nil || ctx.Err() != nil {
				return call, failure
			}
		}
		if ctx.Err() != nil {
			return call, nil
		}
		if call.State == chat.StateSucceeded && (call.CheckpointStage == "tool_end" || !hasTools) {
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
			request := s.contextBuilder.FollowupRequest(call, continuation)
			request.MaxTokens = cfg.MaxOutputTokens
			prepared, failure := s.prepareRequest(r.Provider, request)
			if failure != nil {
				return call, failure
			}
			if call.CheckpointStage == "tool_end" && requestBudgetError(prepared.Body, cfg) != nil {
				base, err := s.execution.OrdinaryRequestBase(ctx, r.RunID, call.ID)
				if err != nil {
					if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
						return call, nil
					}
					return call, persistenceFailure(call, "compact_base", err)
				}
				summary, failure := s.compactor.AfterTools(prepared.Input, base)
				if failure != nil {
					return call, failure
				}
				prefix := ""
				if r.Output != nil {
					prefix = *r.Output
				}
				call = newModelCall(r.Run, cfg.MaxRetry)
				call.Input = summary
				call.CheckpointStage = "compact"
				call.CheckpointContext = prepared.Input.Messages
				call.OutputPrefix = prefix
				continue
			}
			prefix := ""
			if continuation && r.Output != nil {
				prefix = *r.Output
			}
			call = newModelCall(r.Run, cfg.MaxRetry)
			call.Input, call.InputBody = prepared.Input, prepared.Body
			call.Continuation = continuation
			call.OutputPrefix = prefix
		}
		if call.ID == 0 {
			var err error
			if failure := requestBudgetError(call.InputBody, cfg); failure != nil {
				return chat.ModelCall{
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
				if s.runInterrupted(ctx, r, err) {
					return call, nil
				}
				return call, persistenceFailure(call, "model_start", err)
			}
			call = call.WithCallStarted()
		}
		var failure *chat.RunError
		call, failure = s.complete(ctx, r, call)
		r.Output = call.PresentedOutput()
		if failure != nil || ctx.Err() != nil || r.State == chat.StateWaitForCancel {
			return call, failure
		}
		if call.Output == nil || !call.HasToolCalls() && call.Output.FinishReason != "length" {
			return call, nil
		}
		if s.toolStore == nil || call.HasToolCalls() && !s.toolFactory.Configured() {
			return call, &chat.RunError{
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

func toolError(code, message string) *chat.RunError {
	return &chat.RunError{
		Code:    code,
		Message: message,
	}
}

func (s *Run) ordinaryRoundBudgetError(ctx context.Context, call chat.ModelCall, cfg chat.ModelConfig) *chat.RunError {
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

func (s *Run) continuationBudgetError(ctx context.Context, call chat.ModelCall, cfg chat.ModelConfig) *chat.RunError {
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
