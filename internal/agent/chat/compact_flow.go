package chat

import (
	"context"
	"errors"
	"time"
)

func (s *Runtime) advanceCompact(ctx context.Context, r *Record, cfg ModelConfig, call ModelCall) (ModelCall, *RunError) {
	for {
		if ctx.Err() != nil {
			return call, nil
		}
		if call.ID == 0 {
			prepared, failure := s.prepareRequest(r.Provider, call.Input)
			if failure != nil {
				return call, failure
			}
			call.Input, call.InputBody = prepared.Input, prepared.Body
			id, err := s.execution.BeginCall(ctx, call)
			if err != nil {
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
				return call, persistenceFailure(call, "compact_start", err)
			}
			call.ID = id
			call = call.WithCallStarted()
		}
		if call.State != StateSucceeded {
			var failure *RunError
			call, failure = s.complete(ctx, *r, call)
			if failure != nil || ctx.Err() != nil {
				return call, failure
			}
		}
		if !s.compactor.validResponse(call.Output) {
			return call, toolError("model_invalid_format", "摘要响应必须为正文或思考，不允许调用工具")
		}
		latest, available := s.currentModel(r.Provider, r.Model)
		if latest.ContextWindow > latest.MaxOutputTokens && latest.MaxOutputTokens > 0 {
			cfg = latest
		}
		if call.Output.FinishReason == "length" {
			accepted := call.WithCompactResponse()
			if call.State != StateSucceeded {
				if err := s.toolStore.AcceptResponse(s.persistenceContext(), accepted); err != nil {
					return call, persistenceFailure(call, "compact_response", err)
				}
				accepted.PendingEvents = nil
			}
			call = accepted
			if !available {
				return call, toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
			}
			if failure := s.continuationBudgetError(ctx, call, cfg); failure != nil || ctx.Err() != nil {
				return call, failure
			}
			request := s.contextBuilder.followupRequest(call, true)
			request.MaxTokens = cfg.MaxOutputTokens
			call = ModelCall{
				RunID:             r.RunID,
				SessionID:         r.SessionID,
				Input:             request,
				State:             StateRunning,
				MaxRetry:          cfg.MaxRetry,
				Continuation:      true,
				CheckpointStage:   "compact",
				CheckpointContext: call.CheckpointContext,
				OutputPrefix:      call.OutputPrefix,
			}
			continue
		}
		ordinary, err := s.contextBuilder.firstRequest(ctx, *r, cfg)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return call, nil
			}
			return call, persistenceFailure(call, "compact_history", err)
		}
		summary := s.compactor.summary(call)
		if summary == "" {
			return call, toolError("model_invalid_format", "摘要未返回有效正文")
		}
		ordinary = s.contextBuilder.withSummary(ordinary, summary)
		projected := call.WithCheckpoint("compact", CheckpointState{Context: ordinary.Messages})
		projected.State, projected.Error = StateSucceeded, nil
		var failure *RunError
		var prepared PreparedRequest
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
		var next *ModelCall
		var terminal *Record
		if failure == nil {
			next = &ModelCall{
				RunID:     r.RunID,
				SessionID: r.SessionID,
				Input:     prepared.Input,
				InputBody: prepared.Body,
				State:     StateRunning,
				MaxRetry:  cfg.MaxRetry,
			}
		} else {
			finished := *r
			finished.State, finished.Error = StateFailed, failure
			now := time.Now()
			finished.FinishedAt = &now
			terminal = &finished
		}
		committed, err := s.execution.CompleteCompact(ctx, projected, next, terminal)
		if err != nil {
			// 提交回执丢失时沿实际 checkpoint 收尾，不能重发已经登记的普通调用或丢弃有效摘要。
			if saved, readErr := s.execution.ReadCall(s.persistenceContext(), r.RunID); readErr == nil && saved != nil && (saved.ID != call.ID || saved.State == StateSucceeded) {
				call = *saved
			}
			// 取消事务可能先于本地 context 信号完成；以已提交取消事实识别切换门禁。
			if errors.Is(err, context.Canceled) {
				if saved, readErr := s.runs.ReadRun(s.persistenceContext(), r.RunID, false); readErr == nil && saved.State == StateWaitForCancel {
					r.State = saved.State
					return call, nil
				}
			}
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return call, nil
			}
			return call, persistenceFailure(call, "compact_complete", err)
		}
		return committed, failure
	}
}
