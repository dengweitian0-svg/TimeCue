package loop

import (
	"context"
	"errors"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

func (s *Run) advanceCompact(ctx context.Context, r *chat.Record, cfg chat.ModelConfig, call chat.ModelCall) (chat.ModelCall, *chat.RunError) {
	for {
		if ctx.Err() != nil {
			return call, nil
		}
		if call.ID == 0 {
			prepared, failure := s.prepareRequest(r.Provider, call.Input)
			if failure != nil {
				return call, failure
			}
			if failure := requestBudgetError(prepared.Body, cfg); failure != nil {
				return call, failure
			}
			call.Input, call.InputBody = prepared.Input, prepared.Body
			id, err := s.execution.BeginCall(ctx, call)
			if err != nil {
				if s.runInterrupted(ctx, r, err) {
					return call, nil
				}
				return call, persistenceFailure(call, "compact_start", err)
			}
			call.ID = id
			call = call.WithCallStarted()
		}
		if call.State != chat.StateSucceeded {
			var failure *chat.RunError
			call, failure = s.complete(ctx, r, call)
			if failure != nil || ctx.Err() != nil || r.State == chat.StateWaitForCancel {
				return call, failure
			}
		}
		if !s.compactor.ValidResponse(call.Output) {
			return call, toolError("model_invalid_format", "摘要响应必须为正文或思考，不允许调用工具")
		}
		latest, modelAvailable := s.currentModel(r.Provider, r.Model)
		if latest.ContextWindow > latest.MaxOutputTokens && latest.MaxOutputTokens > 0 {
			cfg = latest
		}
		if call.Output.FinishReason == "length" {
			accepted := call.WithCompactResponse()
			if call.State != chat.StateSucceeded {
				if err := s.toolStore.AcceptResponse(s.persistenceContext(), accepted); err != nil {
					return call, persistenceFailure(call, "compact_response", err)
				}
				accepted.PendingEvents = nil
			}
			call = accepted
			if !modelAvailable {
				return call, toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
			}
			if failure := s.continuationBudgetError(ctx, call, cfg); failure != nil || ctx.Err() != nil {
				return call, failure
			}
			request := s.contextBuilder.FollowupRequest(call, true)
			request.MaxTokens = cfg.MaxOutputTokens
			continuation := newModelCall(r.Run, cfg.MaxRetry)
			continuation.Input = request
			continuation.Continuation = true
			continuation.CheckpointStage = "compact"
			continuation.CheckpointContext = call.CheckpointContext
			continuation.OutputPrefix = call.OutputPrefix
			call = continuation
			continue
		}
		ordinary, err := s.contextBuilder.FirstRequest(ctx, *r, cfg)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return call, nil
			}
			return call, persistenceFailure(call, "compact_history", err)
		}
		summary := s.compactor.Summary(call)
		if summary == "" {
			return call, toolError("model_invalid_format", "摘要未返回有效正文")
		}
		ordinary = s.contextBuilder.WithSummary(ordinary, summary)
		projected := call.WithCheckpoint("compact", chat.CheckpointState{Context: ordinary.Messages})
		projected.State, projected.Error = chat.StateSucceeded, nil
		var failure *chat.RunError
		var prepared chat.PreparedRequest
		if cfg.ContextWindow <= cfg.MaxOutputTokens || cfg.MaxOutputTokens <= 0 {
			// 原模型预算已移除时仍保存完整成功摘要，随后以配置不可用结束。
			failure = toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
		} else {
			prepared, failure = s.prepareRequest(r.Provider, ordinary)
			if failure == nil {
				failure = requestBudgetError(prepared.Body, cfg)
			}
		}
		if failure == nil {
			failure = s.ordinaryRoundBudgetError(ctx, call, cfg)
			if ctx.Err() != nil || failure != nil && failure.Code == "persistence_failed" {
				return call, failure
			}
		}
		var next *chat.ModelCall
		var terminal *chat.Record
		if failure == nil {
			ordinaryCall := newModelCall(r.Run, cfg.MaxRetry)
			ordinaryCall.Input, ordinaryCall.InputBody = prepared.Input, prepared.Body
			next = &ordinaryCall
		} else {
			finished := *r
			finished.State, finished.Error = chat.StateFailed, failure
			now := time.Now()
			finished.FinishedAt = &now
			terminal = &finished
		}
		committed, err := s.execution.CompleteCompact(ctx, projected, next, terminal)
		if err != nil {
			// 提交回执丢失时沿实际 checkpoint 收尾，不能重发已经登记的普通调用或丢弃有效摘要。
			if saved, readErr := s.execution.ReadCall(s.persistenceContext(), r.RunID); readErr == nil && saved != nil && (saved.ID != call.ID || saved.State == chat.StateSucceeded) {
				call = *saved
			}
			if s.runInterrupted(ctx, r, err) {
				return call, nil
			}
			return call, persistenceFailure(call, "compact_complete", err)
		}
		return committed, failure
	}
}
