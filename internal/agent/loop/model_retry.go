package loop

import (
	"context"
	"slices"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

func (s *Run) currentModel(provider, model string) (chat.ModelConfig, bool) {
	configs, err := s.settings()
	if err != nil {
		return chat.ModelConfig{}, false
	}
	for _, cfg := range configs {
		if cfg.Provider == provider && cfg.Model == model {
			return cfg, available(cfg)
		}
	}
	return chat.ModelConfig{}, false
}

func (s *Run) complete(ctx context.Context, r *chat.Record, call chat.ModelCall) (chat.ModelCall, *chat.RunError) {
	for ctx.Err() == nil {
		action, at := recoverModel(call)
		switch action {
		case reuseModelReply:
			return call, call.Output.ValidateResponse()
		case waitForModelRetry:
			if _, ok := s.currentModel(r.Provider, r.Model); !ok {
				return call, toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
			}
			if !waitUntil(ctx, at) {
				return call, nil
			}
			started := call
			started.State, started.Error, started.NextRetryAt = chat.StateRunning, nil, nil
			started.RetryHistory = slices.Clone(call.RetryHistory)
			if len(started.RetryHistory) > 0 {
				started.RetryHistory[len(started.RetryHistory)-1].Result = "running"
			}
			if err := s.execution.SaveEvents(ctx, started); err != nil {
				if s.runInterrupted(ctx, r, err) {
					return call, nil
				}
				return call, persistenceFailure(call, "model_retry_start", err)
			}
			call = started.WithOutputSaved()
		}
		// 密钥和超时每次实际请求前重读；逻辑调用的请求 input 与重试预算固定。
		cfg, ok := s.currentModel(r.Provider, r.Model)
		if !ok {
			return call, &chat.RunError{
				Code:    "model_config_unavailable",
				Message: "原运行的模型配置或密钥不可用",
			}
		}
		if ctx.Err() != nil {
			return call, nil
		}
		modelCtx, cancel := context.WithCancel(ctx)
		previousOutput := call.Output != nil && chat.HasAssistantContent(call.Output.Message)
		batch := &eventBatch{
			repository: s.execution,
			context:    s.persistenceContext,
			call:       call,
			partial: chat.ModelOutput{
				Message: chat.Message{Role: "assistant"},
			},
			cancel: cancel,
		}
		output, failure := s.model.Complete(modelCtx, cfg, chat.PreparedRequest{
			Body:  call.InputBody,
			Input: call.Input,
		}, batch.append)
		var err error
		call, err = batch.close()
		cancel()
		if err != nil {
			failure = persistenceFailure(call, "model_events", err)
		}
		hasOutput := chat.HasAssistantContent(output.Message)
		// 生命周期恢复没有新输出时保留旧快照；完整空响应仍替换旧回复。
		if failure == nil && ctx.Err() == nil || !previousOutput || hasOutput {
			call.Output = &output
		}
		if len(call.RetryHistory) > 0 {
			last := &call.RetryHistory[len(call.RetryHistory)-1]
			last.Error = failure
			last.Result = "succeeded"
			if failure != nil {
				last.Result = "failed"
			}
			if ctx.Err() != nil {
				last.Result = "interrupted"
				last.Error = nil
			}
		}
		next, retry := nextModelRetry(call, failure, ctx.Err() != nil || hasOutput || previousOutput)
		if !retry {
			return call, failure
		}
		call.RetryCount++
		call.State, call.Error, call.NextRetryAt = chat.StateRetryWait, failure, &next
		call.RetryHistory = append(call.RetryHistory, chat.ModelRetry{
			Retry:  call.RetryCount,
			At:     next,
			Result: "retry_wait",
			Cause:  failure,
		})
		if err := s.execution.SaveEvents(s.persistenceContext(), call); err != nil {
			return call, persistenceFailure(call, "model_retry_wait", err)
		}
		call = call.WithOutputSaved()
		call.PendingEvents = nil
	}
	return call, nil
}
