package chat

import (
	"context"
	"errors"
	"time"
)

func (s *Runtime) currentModel(provider, model string) (ModelConfig, bool) {
	configs, err := s.settings()
	if err != nil {
		return ModelConfig{}, false
	}
	for _, cfg := range configs {
		if cfg.Provider == provider && cfg.Model == model {
			return cfg, available(cfg)
		}
	}
	return ModelConfig{}, false
}

func retryable(failure *RunError) bool {
	if failure == nil {
		return false
	}
	switch failure.Code {
	case "model_timeout", "model_network_error", "model_rate_limited", "model_server_error", "model_server_busy":
		return true
	default:
		return false
	}
}

func retryDelay(retry int) time.Duration {
	delay := 5 * time.Second
	for i := 1; i < retry; i++ {
		// 防止极大配置造成 duration 溢出后退避变为负数。
		if delay > time.Duration(1<<62-1) {
			return time.Duration(1<<63 - 1)
		}
		delay *= 2
	}
	return delay
}

func persistenceFailure() *RunError {
	return &RunError{
		Code:    "persistence_failed",
		Message: "保存模型调用进度失败",
	}
}

func (s *Runtime) complete(ctx context.Context, r Record, call ModelCall) (ModelCall, *RunError) {
	for ctx.Err() == nil {
		if call.State == StateRetryWait {
			if call.NextRetryAt != nil {
				timer := time.NewTimer(time.Until(*call.NextRetryAt))
				select {
				case <-ctx.Done():
					timer.Stop()
					return call, nil
				case <-timer.C:
				}
			}
			if ctx.Err() != nil {
				return call, nil
			}
			call.State, call.Error, call.NextRetryAt = StateRunning, nil, nil
			if len(call.RetryHistory) > 0 {
				call.RetryHistory[len(call.RetryHistory)-1].Result = "running"
			}
			if err := s.execution.SaveEvents(ctx, call); err != nil {
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
				return call, persistenceFailure()
			}
		}
		// 密钥和超时每次实际请求前重读；逻辑调用的请求 input 与重试预算固定。
		cfg, ok := s.currentModel(r.Provider, r.Model)
		if !ok {
			return call, &RunError{
				Code:    "model_config_unavailable",
				Message: "原运行的模型配置或密钥不可用",
			}
		}
		if ctx.Err() != nil {
			return call, nil
		}
		modelCtx, cancel := context.WithCancel(ctx)
		previousOutput := call.Output != nil && hasModelOutput(*call.Output)
		batch := &eventBatch{
			repository: s.execution,
			call:       call,
			partial: ModelOutput{
				Message: Message{Role: "assistant"},
			},
			cancel: cancel,
		}
		output, failure := s.model.Complete(modelCtx, cfg, call.Input, batch.append)
		var err error
		call, err = batch.close()
		cancel()
		if err != nil {
			failure = persistenceFailure()
		}
		hasOutput := hasModelOutput(output)
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
		if ctx.Err() != nil || !retryable(failure) || hasOutput || previousOutput || call.RetryCount >= call.MaxRetry {
			return call, failure
		}
		call.RetryCount++
		next := time.Now().Add(retryDelay(call.RetryCount))
		// SQLite 的纳秒时间不能超过 int64 上限。
		if latest := time.Unix(0, 1<<63-1); next.After(latest) {
			next = latest
		}
		call.State, call.Error, call.NextRetryAt = StateRetryWait, failure, &next
		call.RetryHistory = append(call.RetryHistory, ModelRetry{
			Retry:  call.RetryCount,
			At:     next,
			Result: "retry_wait",
			Cause:  failure,
		})
		if err := s.execution.SaveEvents(context.Background(), call); err != nil {
			return call, persistenceFailure()
		}
		call.PendingEvents = nil
	}
	return call, nil
}
